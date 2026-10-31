// Package compose loads Compose files and computes service policy.
//
// Responsibilities (phase 1), per docs/stacks.md §3:
//   - load compose + override files via github.com/compose-spec/compose-go/v2
//   - enumerate the effective service list
//   - apply allow/exclude to produce the managed set (see internal/stack)
package compose
