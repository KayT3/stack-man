package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// FileName is the repo-local command definition file.
const FileName = ".stackman.yaml"

// File is the parsed .stackman.yaml document.
type File struct {
	Version  int            `yaml:"version"`
	OnPull   string         `yaml:"on_pull"`
	Commands map[string]Def `yaml:"commands"`
}

// Def is one declarative command.
type Def struct {
	Description string     `yaml:"description"`
	Params      []ParamDef `yaml:"params"`
	Steps       []YAMLStep `yaml:"steps"`
}

// ParamDef is a declared command input.
type ParamDef struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     string   `yaml:"default"`
	Allowed     []string `yaml:"allowed"`
}

// YAMLStep is a single YAML step. Exactly one key (compose/exec/shell/git) is set.
type YAMLStep struct {
	Type   string
	Config map[string]any
}

// UnmarshalYAML decodes a single-key step object.
func (s *YAMLStep) UnmarshalYAML(value *yaml.Node) error {
	var raw map[string]map[string]any
	if err := value.Decode(&raw); err != nil {
		return fmt.Errorf("step must be a mapping: %w", err)
	}
	if len(raw) != 1 {
		return fmt.Errorf("step must contain exactly one step type, got %d", len(raw))
	}
	for typ, cfg := range raw {
		s.Type = typ
		s.Config = cfg
		if s.Config == nil {
			s.Config = map[string]any{}
		}
	}
	return nil
}

// Load reads dir/.stackman.yaml. A missing file returns (nil, nil).
func Load(dir string) (*File, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("commands: read %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("commands: parse %s: %w", path, err)
	}
	if f.Version != 0 && f.Version != 1 {
		return nil, fmt.Errorf("commands: %s: unsupported version %d", path, f.Version)
	}
	if f.Commands == nil {
		f.Commands = map[string]Def{}
	}
	for name, def := range f.Commands {
		if name == "" {
			return nil, fmt.Errorf("commands: %s: empty command name", path)
		}
		if len(def.Steps) == 0 {
			return nil, fmt.Errorf("commands: %s: command %q has no steps", path, name)
		}
		for i, step := range def.Steps {
			if step.Type == "" {
				return nil, fmt.Errorf("commands: %s: command %q step %d: empty step type", path, name, i+1)
			}
		}
	}
	return &f, nil
}

// Names returns command names sorted.
func (f *File) Names() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.Commands))
	for name := range f.Commands {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Get returns a command definition.
func (f *File) Get(name string) (Def, bool) {
	if f == nil {
		return Def{}, false
	}
	def, ok := f.Commands[name]
	return def, ok
}

// Spec converts a definition's params into the registry's Param shape.
func (d Def) Spec() CommandSpec {
	spec := CommandSpec{Params: make([]Param, 0, len(d.Params))}
	for _, p := range d.Params {
		spec.Params = append(spec.Params, Param{
			Name:        p.Name,
			Description: p.Description,
			Required:    p.Required,
			Default:     p.Default,
			Allowed:     p.Allowed,
		})
	}
	return spec
}
