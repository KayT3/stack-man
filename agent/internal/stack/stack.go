// Package stack holds the domain model for registered stacks and the
// allow/exclude service-policy logic (docs/stacks.md).
package stack

import "time"

// Policy values persisted in the stack_services table.
const (
	PolicyAllow   = "allow"
	PolicyExclude = "exclude"
)

// Stack is a registered directory on a VPS: a git repo + a Compose file.
// It maps to the pkg/api.Stack wire DTO when crossing the RPC boundary.
type Stack struct {
	ID          string
	Name        string
	Path        string
	Remote      string
	Branch      string
	ComposeFile string
	Allow       []string
	Exclude     []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ServicePolicy is one Compose service and its effective policy.
type ServicePolicy struct {
	Service string
	Policy  string // PolicyAllow, PolicyExclude, or "" (unspecified)
}

// Managed returns the set of services the agent may touch for s:
// allowed = Allow when non-empty, else all; managed = allowed − Exclude.
func (s *Stack) Managed(allServices []string) []string {
	return EffectiveManaged(allServices, s.Allow, s.Exclude)
}

// EffectiveManaged computes the managed set from the full service list and the
// allow/exclude lists. Pure and unit-testable.
func EffectiveManaged(allServices, allow, exclude []string) []string {
	allowed := allServices
	if len(allow) > 0 {
		allowed = allow
	}
	ex := make(map[string]struct{}, len(exclude))
	for _, s := range exclude {
		ex[s] = struct{}{}
	}
	out := make([]string, 0, len(allowed))
	for _, s := range allowed {
		if _, skip := ex[s]; skip {
			continue
		}
		out = append(out, s)
	}
	return out
}
