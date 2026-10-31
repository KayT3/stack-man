// Package git abstracts git operations so callers never shell out directly
// (docs/stacks.md §6). The default implementation drives the system `git`
// binary; the interface lets you swap in go-git later.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client is the git interface used by the daemon and CLI.
type Client interface {
	// Status returns `git status --porcelain=v1` output.
	Status(ctx context.Context, dir string) (string, error)
	// Fetch fetches the remote branch.
	Fetch(ctx context.Context, dir, remote, branch string) error
	// Check is a read-only dry-run answering "is it safe to pull, and what
	// will change?".
	Check(ctx context.Context, dir, remote, branch string) (CheckResult, error)
	// Pull fast-forwards the branch; it must refuse unsafe pulls.
	Pull(ctx context.Context, dir, remote, branch string) (PullResult, error)
}

// CheckResult reports whether a pull is safe and what it would change.
type CheckResult struct {
	Safe         bool
	Clean        bool
	Ahead        int
	Behind       int
	Diverged     bool
	ChangedFiles []string
	Branch       string
	Head         string
	RemoteHead   string
}

// PullResult reports a successful fast-forward.
type PullResult struct {
	Pulled bool
	From   string
	To     string
}

// ErrUnsafePull is returned when a pull would overwrite local work or need a
// merge. The working tree is left untouched.
var ErrUnsafePull = errors.New("git: unsafe pull")

// ExecClient runs the system `git` binary.
type ExecClient struct {
	// Binary is the git executable; defaults to "git".
	Binary string
}

// New returns a Client backed by the system git binary.
func New() *ExecClient {
	return &ExecClient{Binary: "git"}
}

// Status returns porcelain status plus branch/upstream information.
func (c *ExecClient) Status(ctx context.Context, dir string) (string, error) {
	out, _, err := c.run(ctx, dir, "status", "--porcelain=v1", "-b")
	return out, err
}

// Fetch fetches remote/branch. Empty remote means all remotes; empty branch
// means the remote's default branch.
func (c *ExecClient) Fetch(ctx context.Context, dir, remote, branch string) error {
	switch {
	case remote == "":
		_, _, err := c.run(ctx, dir, "fetch", "--all", "--prune")
		return err
	case branch == "":
		_, _, err := c.run(ctx, dir, "fetch", remote)
		return err
	default:
		_, _, err := c.run(ctx, dir, "fetch", remote, branch)
		return err
	}
}

// Check fetches and reports whether a fast-forward pull is safe.
func (c *ExecClient) Check(ctx context.Context, dir, remote, branch string) (CheckResult, error) {
	var res CheckResult
	if err := c.Fetch(ctx, dir, remote, branch); err != nil {
		return res, err
	}

	status, _, err := c.run(ctx, dir, "status", "--porcelain=v1")
	if err != nil {
		return res, err
	}
	res.Clean = strings.TrimSpace(status) == ""

	counts, _, err := c.run(ctx, dir, "rev-list", "--left-right", "--count", "HEAD...FETCH_HEAD")
	if err != nil {
		return res, err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return res, fmt.Errorf("git: unexpected rev-list output %q", strings.TrimSpace(counts))
	}
	res.Ahead, err = strconv.Atoi(fields[0])
	if err != nil {
		return res, fmt.Errorf("git: parse ahead count: %w", err)
	}
	res.Behind, err = strconv.Atoi(fields[1])
	if err != nil {
		return res, fmt.Errorf("git: parse behind count: %w", err)
	}
	res.Diverged = res.Ahead > 0 && res.Behind > 0

	changed, _, err := c.run(ctx, dir, "diff", "--name-only", "HEAD...FETCH_HEAD")
	if err != nil {
		return res, err
	}
	for _, line := range strings.Split(changed, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			res.ChangedFiles = append(res.ChangedFiles, line)
		}
	}

	res.Branch, _, err = c.revParse(ctx, dir, "--abbrev-ref", "HEAD")
	if err != nil {
		return res, err
	}
	res.Head, _, err = c.revParse(ctx, dir, "HEAD")
	if err != nil {
		return res, err
	}
	res.RemoteHead, _, err = c.revParse(ctx, dir, "FETCH_HEAD")
	if err != nil {
		return res, err
	}
	res.Safe = res.Clean && !res.Diverged && res.Behind > 0
	return res, nil
}

// Pull fast-forwards only when Check says it is safe. Dirty trees and
// divergence are refused without changing the working tree.
func (c *ExecClient) Pull(ctx context.Context, dir, remote, branch string) (PullResult, error) {
	var res PullResult
	var check CheckResult
	err := c.withLock(dir, func() error {
		var err error
		check, err = c.Check(ctx, dir, remote, branch)
		if err != nil {
			return err
		}
		res.From = check.Head
		res.To = check.RemoteHead

		switch {
		case !check.Clean:
			return fmt.Errorf("%w: local working tree is dirty; commit or stash changes on the VPS and retry", ErrUnsafePull)
		case check.Diverged:
			return fmt.Errorf("%w: local branch %s has diverged (ahead %d, behind %d); resolve it manually on the VPS and retry", ErrUnsafePull, check.Branch, check.Ahead, check.Behind)
		case check.Behind == 0:
			// Already up to date: a no-op success.
			return nil
		}

		if _, stderr, err := c.run(ctx, dir, "merge", "--ff-only", "FETCH_HEAD"); err != nil {
			return fmt.Errorf("git: fast-forward: %w: %s", err, strings.TrimSpace(stderr))
		}
		res.Pulled = true
		head, _, err := c.revParse(ctx, dir, "HEAD")
		if err == nil {
			res.To = head
		}
		return nil
	})
	return res, err
}

// withLock serializes git operations per repository. A lock file inside .git
// avoids a cross-platform flock dependency.
func (c *ExecClient) withLock(dir string, fn func() error) error {
	gitDir := filepath.Join(dir, ".git")
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		// Worktrees have a .git file; keep the lock beside it instead.
		gitDir = dir
	}
	lockPath := filepath.Join(gitDir, "stackman-git.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("git: another stackman git operation is in progress (%s)", lockPath)
		}
		return fmt.Errorf("git: create lock: %w", err)
	}
	defer func() {
		file.Close()
		os.Remove(lockPath)
	}()
	fmt.Fprintf(file, "pid %d at %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	return fn()
}

func (c *ExecClient) revParse(ctx context.Context, dir string, args ...string) (string, string, error) {
	full := append([]string{"rev-parse"}, args...)
	return c.run(ctx, dir, full...)
}

func (c *ExecClient) run(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	binary := c.Binary
	if binary == "" {
		binary = "git"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), strings.TrimSpace(errBuf.String()),
			fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out.String()), strings.TrimSpace(errBuf.String()), nil
}
