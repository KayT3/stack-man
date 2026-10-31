package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"

	"github.com/example/stack-man/agent/internal/compose"
)

// UpOptions controls `containers up`.
type UpOptions struct {
	Build         bool
	ForceRecreate bool
	RemoveOrphans bool
	Services      []string
}

// DownOptions controls `containers down`.
type DownOptions struct {
	Volumes  bool
	Services []string
}

// ContainerStatus is one row of `containers ps`.
type ContainerStatus struct {
	Name    string
	Service string
	State   string
	Status  string
}

// ExecResult is the result of `containers exec`.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Network is one Docker network.
type Network struct {
	Name   string
	Driver string
	Scope  string
}

// ContainersUp starts the stack's managed services.
func (s *Service) ContainersUp(ctx context.Context, name string, opts UpOptions, out io.Writer) error {
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return err
	}
	services, err := sc.target(opts.Services)
	if err != nil {
		return err
	}
	args := []string{"up", "-d"}
	if opts.Build {
		args = append(args, "--build")
	}
	if opts.ForceRecreate {
		args = append(args, "--force-recreate")
	}
	if opts.RemoveOrphans {
		args = append(args, "--remove-orphans")
	}
	args = append(args, services...)
	return compose.Run(ctx, sc.Stack.Path, sc.Files, out, args...)
}

// ContainersDown stops the stack's managed services. Excluded services are
// left running unless explicitly named (which target() refuses).
func (s *Service) ContainersDown(ctx context.Context, name string, opts DownOptions, out io.Writer) error {
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return err
	}
	services, err := sc.target(opts.Services)
	if err != nil {
		return err
	}
	args := []string{"down"}
	if opts.Volumes {
		args = append(args, "--volumes")
	}
	args = append(args, services...)
	return compose.Run(ctx, sc.Stack.Path, sc.Files, out, args...)
}

// ContainersRestart restarts the stack's managed services.
func (s *Service) ContainersRestart(ctx context.Context, name string, services []string, out io.Writer) error {
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return err
	}
	target, err := sc.target(services)
	if err != nil {
		return err
	}
	args := append([]string{"restart"}, target...)
	return compose.Run(ctx, sc.Stack.Path, sc.Files, out, args...)
}

// ContainersBuild builds images for the stack's managed services.
func (s *Service) ContainersBuild(ctx context.Context, name string, services []string, out io.Writer) error {
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return err
	}
	target, err := sc.target(services)
	if err != nil {
		return err
	}
	args := append([]string{"build"}, target...)
	return compose.Run(ctx, sc.Stack.Path, sc.Files, out, args...)
}

// ContainersPs returns the stack's containers.
func (s *Service) ContainersPs(ctx context.Context, name string) ([]ContainerStatus, error) {
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return nil, err
	}
	args := composeArgs(sc.Files, "ps", "--all", "--format", "json")
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = sc.Stack.Path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker compose ps: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parsePS(stdout.Bytes())
}

// ContainersExec runs a command in one managed service.
func (s *Service) ContainersExec(ctx context.Context, name, service string, argv []string) (ExecResult, error) {
	var res ExecResult
	if len(argv) == 0 {
		return res, errors.New("containers exec: command is required")
	}
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return res, err
	}
	if _, err := sc.target([]string{service}); err != nil {
		return res, err
	}
	args := composeArgs(sc.Files, "exec", "-T", service)
	args = append(args, argv...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = sc.Stack.Path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, fmt.Errorf("containers exec: exit code %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
		}
		return res, fmt.Errorf("containers exec: %w", err)
	}
	return res, nil
}

// ContainersLogs streams `docker compose logs`.
func (s *Service) ContainersLogs(ctx context.Context, name, service string, follow bool, out io.Writer) error {
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return err
	}
	args := []string{"logs", "--no-color"}
	if follow {
		args = append(args, "--follow")
	}
	if service != "" {
		if _, err := sc.target([]string{service}); err != nil {
			return err
		}
		args = append(args, service)
	}
	return compose.Run(ctx, sc.Stack.Path, sc.Files, out, args...)
}

// ContainersNetworks lists Docker networks visible on the host.
func (s *Service) ContainersNetworks(ctx context.Context, name string) ([]Network, error) {
	if _, err := s.stackCompose(ctx, name); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "docker", "network", "ls", "--format", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker network ls: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out []Network
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw struct {
			Name   string `json:"Name"`
			Driver string `json:"Driver"`
			Scope  string `json:"Scope"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		out = append(out, Network{Name: raw.Name, Driver: raw.Driver, Scope: raw.Scope})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// NetworkConnect attaches the managed containers of services to network.
func (s *Service) NetworkConnect(ctx context.Context, name, network string, services []string, out io.Writer) error {
	return s.networkOp(ctx, name, network, services, out, "connect")
}

// NetworkDisconnect detaches the managed containers of services from network.
func (s *Service) NetworkDisconnect(ctx context.Context, name, network string, services []string, out io.Writer) error {
	return s.networkOp(ctx, name, network, services, out, "disconnect")
}

func (s *Service) networkOp(ctx context.Context, name, network string, services []string, out io.Writer, verb string) error {
	if strings.TrimSpace(network) == "" {
		return errors.New("network name is required")
	}
	sc, err := s.stackCompose(ctx, name)
	if err != nil {
		return err
	}
	target, err := sc.target(services)
	if err != nil {
		return err
	}
	containers, err := s.ContainersPs(ctx, name)
	if err != nil {
		return err
	}
	byService := map[string][]string{}
	for _, c := range containers {
		byService[c.Service] = append(byService[c.Service], c.Name)
	}
	for _, service := range target {
		names := byService[service]
		if len(names) == 0 {
			return fmt.Errorf("no container found for service %q", service)
		}
		for _, container := range names {
			cmd := exec.CommandContext(ctx, "docker", "network", verb, network, container)
			cmd.Stdout = out
			cmd.Stderr = out
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("docker network %s %s %s: %w", verb, network, container, err)
			}
		}
	}
	return nil
}

func composeArgs(files []string, args ...string) []string {
	out := []string{"compose"}
	for _, file := range files {
		out = append(out, "-f", file)
	}
	return append(out, args...)
}

func parsePS(data []byte) ([]ContainerStatus, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	type rawPS struct {
		Name    string `json:"Name"`
		Service string `json:"Service"`
		State   string `json:"State"`
		Status  string `json:"Status"`
	}
	var raws []rawPS

	// Compose v2 versions disagree on whether --format json emits an array or
	// one object per line. Support both.
	if data[0] == '[' {
		if err := json.Unmarshal(data, &raws); err != nil {
			return nil, fmt.Errorf("parse compose ps: %w", err)
		}
	} else {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var one rawPS
			if err := json.Unmarshal([]byte(line), &one); err != nil {
				return nil, fmt.Errorf("parse compose ps: %w", err)
			}
			raws = append(raws, one)
		}
	}
	out := make([]ContainerStatus, 0, len(raws))
	for _, r := range raws {
		out = append(out, ContainerStatus{Name: r.Name, Service: r.Service, State: r.State, Status: r.Status})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service == out[j].Service {
			return out[i].Name < out[j].Name
		}
		return out[i].Service < out[j].Service
	})
	return out, nil
}
