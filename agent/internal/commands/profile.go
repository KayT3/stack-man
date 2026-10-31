package commands

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Profile is a repository's command set, compiled into the agent. Profiles are
// keyed by the normalized git remote (see NormalizeRemote), so the same repo
// gets the same commands on every VPS regardless of clone URL syntax.
type Profile struct {
	Repo     string   // canonical remote key, e.g. github.com/owner/repo
	Aliases  []string // optional additional remotes that resolve to this profile
	OnPull   string   // command to run after a successful pull (optional)
	Commands map[string]Def
}

// Def is one command in a profile.
type Def struct {
	Description string
	Params      []Param
	Steps       []Step
}

// Spec converts a definition's params into the registry's Param shape.
func (d Def) Spec() CommandSpec {
	params := make([]Param, len(d.Params))
	copy(params, d.Params)
	return CommandSpec{Params: params}
}

// Names returns command names sorted.
func (p *Profile) Names() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Commands))
	for name := range p.Commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Get returns a command definition.
func (p *Profile) Get(name string) (Def, bool) {
	if p == nil {
		return Def{}, false
	}
	def, ok := p.Commands[name]
	return def, ok
}

var profiles = struct {
	mu     sync.RWMutex
	byRepo map[string]*Profile
}{
	byRepo: make(map[string]*Profile),
}

// RegisterProfile adds a compiled-in profile. Invalid profiles and duplicate
// remote keys panic: both are programmer errors caught at agent startup.
func RegisterProfile(p Profile) {
	key := NormalizeRemote(p.Repo)
	if key == "" {
		panic("commands: profile has an invalid repo " + p.Repo)
	}
	p.Repo = key

	keys := []string{key}
	seen := map[string]struct{}{key: {}}
	for _, alias := range p.Aliases {
		aliasKey := NormalizeRemote(alias)
		if aliasKey == "" {
			panic("commands: profile " + p.Repo + " has an invalid alias " + alias)
		}
		if _, dup := seen[aliasKey]; dup {
			continue
		}
		seen[aliasKey] = struct{}{}
		keys = append(keys, aliasKey)
	}

	if p.Commands == nil {
		p.Commands = map[string]Def{}
	}
	for name, def := range p.Commands {
		if strings.TrimSpace(name) == "" {
			panic("commands: profile " + p.Repo + " has an empty command name")
		}
		if len(def.Steps) == 0 {
			panic(fmt.Sprintf("commands: profile %s command %q has no steps", p.Repo, name))
		}
		for i, step := range def.Steps {
			if step == nil || step.StepName() == "" {
				panic(fmt.Sprintf("commands: profile %s command %q step %d is invalid", p.Repo, name, i+1))
			}
		}
	}
	if p.OnPull != "" {
		if _, ok := p.Commands[p.OnPull]; !ok {
			panic(fmt.Sprintf("commands: profile %s on_pull %q is not a defined command", p.Repo, p.OnPull))
		}
	}

	profiles.mu.Lock()
	defer profiles.mu.Unlock()
	for _, k := range keys {
		if _, dup := profiles.byRepo[k]; dup {
			panic("commands: duplicate profile for " + k)
		}
	}
	stored := p
	for _, k := range keys {
		profiles.byRepo[k] = &stored
	}
}

// ProfileFor returns the profile for a git remote, or nil when none matches.
func ProfileFor(remote string) *Profile {
	key := NormalizeRemote(remote)
	if key == "" {
		return nil
	}
	profiles.mu.RLock()
	defer profiles.mu.RUnlock()
	return profiles.byRepo[key]
}

// NormalizeRemote turns a git remote into a stable profile key:
//
//	git@github.com:Owner/Repo.git          -> github.com/owner/repo
//	https://github.com/Owner/Repo/         -> github.com/owner/repo
//	ssh://git@github.com/Owner/Repo.git    -> github.com/owner/repo
//
// Non-remote values (local paths, empty strings) return "".
func NormalizeRemote(remote string) string {
	s := strings.TrimSpace(remote)
	if s == "" {
		return ""
	}

	if !strings.Contains(s, "://") {
		// scp-like syntax: [user@]host:path. Don't mistake Windows drive
		// paths (C:\repo) or local relative paths for remotes.
		i := strings.Index(s, ":")
		if i < 0 {
			// Already-canonical host/path form, e.g. github.com/owner/repo.
			first := s
			if j := strings.Index(s, "/"); j >= 0 {
				first = s[:j]
			}
			if strings.HasPrefix(s, "/") || !strings.Contains(first, ".") {
				return ""
			}
			s = "https://" + s
		} else {
			if i == 0 {
				return ""
			}
			prefix, rest := s[:i], s[i+1:]
			if strings.ContainsAny(prefix, `/\`) || rest == "" ||
				(!strings.Contains(prefix, "@") && !strings.Contains(prefix, ".")) {
				return ""
			}
			s = "ssh://" + prefix + "/" + rest
		}
	}

	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())

	path := strings.ToLower(strings.Trim(u.Path, "/"))
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	return host + "/" + path
}
