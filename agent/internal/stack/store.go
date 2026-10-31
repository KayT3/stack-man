package stack

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Storage errors.
var (
	ErrNotFound = errors.New("stack: not found")
	ErrExists   = errors.New("stack: already registered")
)

// Store is the SQLite-backed stack registry (docs/stacks.md §4).
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the registry at path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("stack: empty database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("stack: create data dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("stack: open database: %w", err)
	}
	// SQLite has one writer; serializing on one connection avoids busy
	// errors between the CLI and daemon.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("stack: pragma: %w", err)
		}
	}

	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("stack: read schema version: %w", err)
	}
	if version > 1 {
		return fmt.Errorf("stack: database schema v%d is newer than this agent supports (v1)", version)
	}
	if version == 0 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE stacks (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  path         TEXT NOT NULL UNIQUE,
  remote       TEXT NOT NULL DEFAULT '',
  branch       TEXT NOT NULL DEFAULT '',
  compose_file TEXT NOT NULL DEFAULT 'docker-compose.yml',
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE TABLE stack_services (
  stack_id TEXT NOT NULL REFERENCES stacks(id) ON DELETE CASCADE,
  service  TEXT NOT NULL,
  policy   TEXT NOT NULL CHECK (policy IN ('allow','exclude')),
  PRIMARY KEY (stack_id, service)
);
`); err != nil {
			return fmt.Errorf("stack: create schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("stack: set schema version: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("stack: commit schema: %w", err)
		}
	}
	return nil
}

// Save inserts or updates a stack. Re-adding the same name/path is idempotent
// and keeps the original ID and creation time.
func (s *Store) Save(ctx context.Context, st *Stack) error {
	if err := validate(st); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	if st.ID == "" {
		st.ID = newID()
	}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = now
	}
	st.UpdatedAt = now

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existingID, existingPath string
	err = tx.QueryRowContext(ctx, "SELECT id, path FROM stacks WHERE name = ?", st.Name).Scan(&existingID, &existingPath)
	switch {
	case err == nil:
		if existingPath != st.Path {
			return fmt.Errorf("%w: name %q is registered to %s", ErrExists, st.Name, existingPath)
		}
		st.ID = existingID
		if _, err := tx.ExecContext(ctx, `
UPDATE stacks SET path = ?, remote = ?, branch = ?, compose_file = ?, updated_at = ?
WHERE id = ?`, st.Path, st.Remote, st.Branch, st.ComposeFile, st.UpdatedAt.Unix(), st.ID); err != nil {
			return fmt.Errorf("stack: update: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		var otherName string
		if err := tx.QueryRowContext(ctx, "SELECT name FROM stacks WHERE path = ?", st.Path).Scan(&otherName); err == nil {
			return fmt.Errorf("%w: path %s is registered as %q", ErrExists, st.Path, otherName)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO stacks (id, name, path, remote, branch, compose_file, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			st.ID, st.Name, st.Path, st.Remote, st.Branch, st.ComposeFile,
			st.CreatedAt.Unix(), st.UpdatedAt.Unix()); err != nil {
			return fmt.Errorf("stack: insert: %w", err)
		}
	default:
		return fmt.Errorf("stack: lookup: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM stack_services WHERE stack_id = ?", st.ID); err != nil {
		return err
	}
	for _, svc := range st.Allow {
		if err := insertPolicy(ctx, tx, st.ID, svc, PolicyAllow); err != nil {
			return err
		}
	}
	for _, svc := range st.Exclude {
		if err := insertPolicy(ctx, tx, st.ID, svc, PolicyExclude); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("stack: commit: %w", err)
	}
	return nil
}

func insertPolicy(ctx context.Context, tx *sql.Tx, stackID, service, policy string) error {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO stack_services (stack_id, service, policy) VALUES (?, ?, ?)
ON CONFLICT(stack_id, service) DO UPDATE SET policy = excluded.policy`,
		stackID, service, policy); err != nil {
		return fmt.Errorf("stack: save policy for %s: %w", service, err)
	}
	return nil
}

func validate(st *Stack) error {
	if strings.TrimSpace(st.Name) == "" {
		return errors.New("stack: name is required")
	}
	if strings.ContainsAny(st.Name, `/\`) {
		return fmt.Errorf("stack: invalid name %q", st.Name)
	}
	if strings.TrimSpace(st.Path) == "" {
		return errors.New("stack: path is required")
	}
	if st.ComposeFile == "" {
		st.ComposeFile = "docker-compose.yml"
	}
	return nil
}

// Get returns one stack by name.
func (s *Store) Get(ctx context.Context, name string) (*Stack, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, path, remote, branch, compose_file, created_at, updated_at
FROM stacks WHERE name = ?`, name)
	st, err := scanStack(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadPolicies(ctx, st); err != nil {
		return nil, err
	}
	return st, nil
}

// List returns all registered stacks, sorted by name.
func (s *Store) List(ctx context.Context) ([]*Stack, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, path, remote, branch, compose_file, created_at, updated_at
FROM stacks ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("stack: list: %w", err)
	}
	defer rows.Close()

	var out []*Stack
	for rows.Next() {
		st, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, st := range out {
		if err := s.loadPolicies(ctx, st); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Remove deletes a stack and its policies.
func (s *Store) Remove(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM stacks WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("stack: remove: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return nil
}

// Count returns the number of registered stacks.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM stacks").Scan(&n)
	return n, err
}

// Services returns the stack's configured policies, sorted by service name.
func (s *Store) Services(ctx context.Context, stackID string) ([]ServicePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT service, policy FROM stack_services WHERE stack_id = ? ORDER BY service`, stackID)
	if err != nil {
		return nil, fmt.Errorf("stack: services: %w", err)
	}
	defer rows.Close()
	var out []ServicePolicy
	for rows.Next() {
		var p ServicePolicy
		if err := rows.Scan(&p.Service, &p.Policy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPolicy sets (or clears, with policy "") one service's policy.
func (s *Store) SetPolicy(ctx context.Context, stackID, service, policy string) error {
	switch policy {
	case PolicyAllow, PolicyExclude:
		_, err := s.db.ExecContext(ctx, `
INSERT INTO stack_services (stack_id, service, policy) VALUES (?, ?, ?)
ON CONFLICT(stack_id, service) DO UPDATE SET policy = excluded.policy`,
			stackID, service, policy)
		return err
	case "":
		_, err := s.db.ExecContext(ctx, "DELETE FROM stack_services WHERE stack_id = ? AND service = ?", stackID, service)
		return err
	default:
		return fmt.Errorf("stack: invalid policy %q (want allow, exclude, or empty)", policy)
	}
}

func (s *Store) loadPolicies(ctx context.Context, st *Stack) error {
	policies, err := s.Services(ctx, st.ID)
	if err != nil {
		return err
	}
	st.Allow = nil
	st.Exclude = nil
	for _, p := range policies {
		switch p.Policy {
		case PolicyAllow:
			st.Allow = append(st.Allow, p.Service)
		case PolicyExclude:
			st.Exclude = append(st.Exclude, p.Service)
		}
	}
	sort.Strings(st.Allow)
	sort.Strings(st.Exclude)
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanStack(row scanner) (*Stack, error) {
	var st Stack
	var created, updated int64
	if err := row.Scan(&st.ID, &st.Name, &st.Path, &st.Remote, &st.Branch, &st.ComposeFile, &created, &updated); err != nil {
		return nil, err
	}
	st.CreatedAt = time.Unix(created, 0).UTC()
	st.UpdatedAt = time.Unix(updated, 0).UTC()
	return &st, nil
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("stack-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
