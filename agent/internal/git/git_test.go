package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "core.hooksPath="}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup creates a bare origin, a seed clone and a work clone with one commit.
func setup(t *testing.T) (seed, work string) {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	remote := filepath.Join(root, "origin.git")
	seed = filepath.Join(root, "seed")
	work = filepath.Join(root, "work")

	gitRun(t, root, "init", "--bare", "-b", "main", remote)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "init", "-b", "main")
	write(t, filepath.Join(seed, "app.txt"), "one\n")
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-m", "initial")
	gitRun(t, seed, "remote", "add", "origin", remote)
	gitRun(t, seed, "push", "-u", "origin", "main")

	gitRun(t, root, "clone", remote, work)
	return seed, work
}

func TestStatusAndCheckAndPull(t *testing.T) {
	seed, work := setup(t)
	ctx := context.Background()
	c := New()

	status, err := c.Status(ctx, work)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(status, "## main") {
		t.Fatalf("status = %q", status)
	}

	// Up to date: pull is not "safe" (nothing behind) but must be a no-op.
	check, err := c.Check(ctx, work, "origin", "main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !check.Clean || check.Behind != 0 || check.Ahead != 0 || check.Safe {
		t.Fatalf("check = %+v", check)
	}
	res, err := c.Pull(ctx, work, "origin", "main")
	if err != nil {
		t.Fatalf("no-op Pull: %v", err)
	}
	if res.Pulled {
		t.Fatalf("expected no-op, got %+v", res)
	}

	// Remote advances.
	write(t, filepath.Join(seed, "app.txt"), "two\n")
	write(t, filepath.Join(seed, "new.txt"), "new\n")
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-m", "update")
	gitRun(t, seed, "push")

	check, err = c.Check(ctx, work, "origin", "main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !check.Safe || !check.Clean || check.Behind != 1 || check.Diverged {
		t.Fatalf("check = %+v", check)
	}
	if len(check.ChangedFiles) != 2 {
		t.Fatalf("changed files = %v", check.ChangedFiles)
	}

	res, err = c.Pull(ctx, work, "origin", "main")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !res.Pulled || res.From == res.To {
		t.Fatalf("pull = %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(work, "app.txt"))
	if strings.TrimSpace(string(data)) != "two" {
		t.Fatalf("work tree not updated: %q", data)
	}
}

func TestPullRefusesDirtyTree(t *testing.T) {
	seed, work := setup(t)
	ctx := context.Background()
	c := New()

	write(t, filepath.Join(seed, "app.txt"), "remote\n")
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-m", "remote")
	gitRun(t, seed, "push")

	write(t, filepath.Join(work, "app.txt"), "local edit\n")
	before := gitRun(t, work, "rev-parse", "HEAD")

	check, err := c.Check(ctx, work, "origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if check.Clean || check.Safe {
		t.Fatalf("check = %+v", check)
	}
	if _, err := c.Pull(ctx, work, "origin", "main"); !errors.Is(err, ErrUnsafePull) {
		t.Fatalf("Pull error = %v, want ErrUnsafePull", err)
	}
	after := gitRun(t, work, "rev-parse", "HEAD")
	if before != after {
		t.Fatal("HEAD changed on refused pull")
	}
	data, _ := os.ReadFile(filepath.Join(work, "app.txt"))
	if string(data) != "local edit\n" {
		t.Fatalf("working tree changed: %q", data)
	}
}

func TestPullRefusesDiverged(t *testing.T) {
	seed, work := setup(t)
	ctx := context.Background()
	c := New()

	write(t, filepath.Join(work, "local.txt"), "local\n")
	gitRun(t, work, "add", ".")
	gitRun(t, work, "commit", "-m", "local")

	write(t, filepath.Join(seed, "remote.txt"), "remote\n")
	gitRun(t, seed, "add", ".")
	gitRun(t, seed, "commit", "-m", "remote")
	gitRun(t, seed, "push")

	check, err := c.Check(ctx, work, "origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !check.Diverged || check.Safe || check.Ahead != 1 || check.Behind != 1 {
		t.Fatalf("check = %+v", check)
	}
	before := gitRun(t, work, "rev-parse", "HEAD")
	if _, err := c.Pull(ctx, work, "origin", "main"); !errors.Is(err, ErrUnsafePull) {
		t.Fatalf("Pull error = %v, want ErrUnsafePull", err)
	}
	if after := gitRun(t, work, "rev-parse", "HEAD"); after != before {
		t.Fatal("HEAD changed on refused pull")
	}
}

func TestLockBlocksConcurrentPull(t *testing.T) {
	_, work := setup(t)
	lock := filepath.Join(work, ".git", "stackman-git.lock")
	if err := os.WriteFile(lock, []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lock)
	c := New()
	_, err := c.Pull(context.Background(), work, "origin", "main")
	if err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("error = %v", err)
	}
}
