// Package git abstracts git operations so callers never shell out directly
// (docs/stacks.md §6). The default implementation drives the system `git`
// binary; the interface lets you swap in go-git later.
package git

import "context"

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
	From string
	To   string
}
