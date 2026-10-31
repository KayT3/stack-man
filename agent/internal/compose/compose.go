// Package compose loads Compose files and computes service policy.
//
// Responsibilities (phase 1), per docs/stacks.md §3:
//   - load compose + override files via github.com/compose-spec/compose-go/v2
//   - enumerate the effective service list
//   - apply allow/exclude to produce the managed set (see internal/stack)
package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/example/stack-man/agent/internal/stack"
)

// Base file names, in lookup order (docs/stacks.md §5).
var baseNames = []string{"compose.yml", "docker-compose.yml"}

// ErrNotFound means no Compose file was found.
var ErrNotFound = errors.New("compose: no compose file found")

// FindFiles resolves the Compose files for dir. When composeFile is non-empty
// it must exist; otherwise compose.yml then docker-compose.yml is tried. A
// matching override file is merged when present.
func FindFiles(dir, composeFile string) ([]string, error) {
	if dir == "" {
		return nil, errors.New("compose: empty directory")
	}
	var base string
	if composeFile != "" {
		base = filepath.Join(dir, composeFile)
		if !fileExists(base) {
			// Allow callers to pass a bare override name that exists.
			if fileExists(filepath.Join(dir, filepath.Base(composeFile))) {
				base = filepath.Join(dir, filepath.Base(composeFile))
			} else {
				return nil, fmt.Errorf("compose: %s not found in %s", composeFile, dir)
			}
		}
	} else {
		for _, name := range baseNames {
			candidate := filepath.Join(dir, name)
			if fileExists(candidate) {
				base = candidate
				break
			}
		}
	}
	if base == "" {
		return nil, fmt.Errorf("%w in %s (looked for %s)", ErrNotFound, dir, strings.Join(baseNames, ", "))
	}

	files := []string{base}
	override := overrideName(filepath.Base(base))
	if override != "" {
		candidate := filepath.Join(dir, override)
		if fileExists(candidate) {
			files = append(files, candidate)
		}
	}
	return files, nil
}

func overrideName(base string) string {
	switch base {
	case "compose.yml":
		return "compose.override.yml"
	case "docker-compose.yml":
		return "docker-compose.override.yml"
	default:
		dot := strings.LastIndex(base, ".")
		if dot <= 0 {
			return ""
		}
		return base[:dot] + ".override" + base[dot:]
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Project is the minimal view the agent needs from a loaded Compose model.
type Project struct {
	Name     string
	Files    []string
	Services []string
}

// Load parses the effective Compose model for dir, honoring overrides,
// profiles, extends, and environment interpolation.
func Load(ctx context.Context, dir, composeFile string) (*Project, error) {
	files, err := FindFiles(dir, composeFile)
	if err != nil {
		return nil, err
	}
	name := sanitizeProjectName(filepath.Base(dir))
	options, err := cli.NewProjectOptions(files,
		cli.WithName(name),
		cli.WithWorkingDirectory(dir),
		cli.WithOsEnv,
	)
	if err != nil {
		return nil, fmt.Errorf("compose: options: %w", err)
	}
	project, err := options.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("compose: load %s: %w", dir, err)
	}
	return &Project{
		Name:     project.Name,
		Files:    files,
		Services: ServiceNames(project),
	}, nil
}

// ServiceNames returns the project's service names, sorted.
func ServiceNames(project *types.Project) []string {
	out := make([]string, 0, len(project.Services))
	for name := range project.Services {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Policies returns every current service with its configured policy. Services
// not in st.Allow/st.Exclude have an empty policy ("unspecified").
func Policies(project *Project, st *stack.Stack) []stack.ServicePolicy {
	allow := toSet(st.Allow)
	exclude := toSet(st.Exclude)
	out := make([]stack.ServicePolicy, 0, len(project.Services))
	for _, service := range project.Services {
		policy := ""
		if _, ok := exclude[service]; ok {
			policy = stack.PolicyExclude
		} else if _, ok := allow[service]; ok {
			policy = stack.PolicyAllow
		}
		out = append(out, stack.ServicePolicy{Service: service, Policy: policy})
	}
	return out
}

// UnknownNames returns configured service names that are not in services.
func UnknownNames(services []string, configured ...[]string) []string {
	known := toSet(services)
	seen := make(map[string]struct{})
	var out []string
	for _, list := range configured {
		for _, name := range list {
			if _, ok := known[name]; ok {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func toSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

func sanitizeProjectName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "stack"
	}
	return out
}

// Run executes `docker compose` for the stack's files, streaming output to out.
// argv is never passed through a shell.
func Run(ctx context.Context, dir string, files []string, out writer, args ...string) error {
	if len(files) == 0 {
		return errors.New("compose: no files")
	}
	composeArgs := []string{"compose"}
	for _, file := range files {
		composeArgs = append(composeArgs, "-f", file)
	}
	composeArgs = append(composeArgs, args...)
	cmd := exec.CommandContext(ctx, "docker", composeArgs...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// writer is kept local so the compose package does not need to import io just
// for the signature (both are satisfied structurally).
type writer interface {
	Write([]byte) (int, error)
}
