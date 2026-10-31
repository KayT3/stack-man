package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/example/stack-man/agent/internal/discovery"
	"github.com/example/stack-man/agent/internal/git"
	"github.com/example/stack-man/agent/internal/service"
	"github.com/example/stack-man/agent/internal/stack"
	"github.com/example/stack-man/agent/internal/version"
	"github.com/example/stack-man/pkg/api"
)

type rpcError struct {
	Code    int
	Message string
	Data    any
}

func (e *rpcError) Error() string { return e.Message }

func rpcErr(code int, message string) *rpcError {
	return &rpcError{Code: code, Message: message}
}

func (s *Server) dispatch(ctx context.Context, method string, raw json.RawMessage, stream *stream) (any, *rpcError) {
	switch method {
	case api.MethodPing:
		return map[string]bool{"pong": true}, nil

	case api.MethodInfo:
		count, err := s.Service.Store.Count(ctx)
		if err != nil {
			return nil, toRPCError(err)
		}
		return api.AgentInfo{
			AgentID:    s.AgentID,
			Hostname:   discovery.HostnameOr("unknown"),
			Version:    version.Version,
			APIVersion: api.APIVersion,
			Subnet:     s.Config.Subnet,
			Uptime:     time.Since(s.Started).Round(time.Second).String(),
			StackCount: count,
		}, nil

	case api.MethodVersion:
		return map[string]any{"version": version.Version, "api_version": api.APIVersion}, nil

	case api.MethodStackList:
		stacks, err := s.Service.ListStacks(ctx)
		if err != nil {
			return nil, toRPCError(err)
		}
		out := make([]api.StackSummary, 0, len(stacks))
		for _, st := range stacks {
			out = append(out, aptStackSummary(st))
		}
		return out, nil

	case api.MethodStackGet:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		st, err := s.Service.GetStack(ctx, p.Name)
		if err != nil {
			return nil, toRPCError(err)
		}
		return apiStack(st), nil

	case api.MethodStackAdd:
		var p struct {
			Path    string   `json:"path"`
			Name    string   `json:"name"`
			Remote  string   `json:"remote"`
			Branch  string   `json:"branch"`
			Allow   []string `json:"allow"`
			Exclude []string `json:"exclude"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		st, _, err := s.Service.AddStack(ctx, service.AddOptions{
			Path: p.Path, Name: p.Name, Remote: p.Remote, Branch: p.Branch,
			Allow: p.Allow, Exclude: p.Exclude,
		})
		if err != nil {
			return nil, toRPCError(err)
		}
		return apiStack(st), nil

	case api.MethodStackRemove:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		if err := s.Service.RemoveStack(ctx, p.Name); err != nil {
			return nil, toRPCError(err)
		}
		return api.OKResult{OK: true}, nil

	case api.MethodStackServices:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		view, err := s.Service.Services(ctx, p.Name)
		if err != nil {
			return nil, toRPCError(err)
		}
		out := make([]api.ServicePolicy, 0, len(view.Policies))
		for _, policy := range view.Policies {
			out = append(out, api.ServicePolicy{Service: policy.Service, Policy: policy.Policy})
		}
		return out, nil

	case api.MethodStackSetPolicy:
		var p struct {
			Name    string `json:"name"`
			Service string `json:"service"`
			Policy  string `json:"policy"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		st, err := s.Service.SetPolicy(ctx, p.Name, p.Service, p.Policy)
		if err != nil {
			return nil, toRPCError(err)
		}
		return apiStack(st), nil

	case api.MethodGitStatus:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		out, err := s.Service.GitStatus(ctx, p.Name)
		if err != nil {
			return nil, toRPCError(err)
		}
		return out, nil

	case api.MethodGitFetch:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		if err := s.Service.GitFetch(ctx, p.Name); err != nil {
			return nil, toRPCError(err)
		}
		return api.OKResult{OK: true}, nil

	case api.MethodGitCheck:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		res, err := s.Service.GitCheck(ctx, p.Name)
		if err != nil {
			return nil, toRPCError(err)
		}
		return api.GitCheck{
			SafeToPull: res.Safe, Clean: res.Clean, Ahead: res.Ahead, Behind: res.Behind,
			Diverged: res.Diverged, ChangedFiles: res.ChangedFiles, Branch: res.Branch,
			Head: res.Head, RemoteHead: res.RemoteHead,
		}, nil

	case api.MethodJobList:
		return s.listJobs(), nil

	case api.MethodGitPull:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack = p.Name
		stream.command = "git-pull"
		return s.runJob(ctx, p.Name, "git-pull", func(ctx context.Context) (any, *rpcError) {
			res, err := s.Service.GitPull(ctx, p.Name, stream, stream.step)
			if err != nil {
				return nil, toRPCError(err)
			}
			return api.GitPullResult{Pulled: res.Pulled, From: res.From, To: res.To}, nil
		})

	case api.MethodContainersUp:
		var p struct {
			Name          string   `json:"name"`
			Build         bool     `json:"build"`
			ForceRecreate bool     `json:"force_recreate"`
			RemoveOrphans bool     `json:"remove_orphans"`
			Services      []string `json:"services"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack, stream.command = p.Name, "up"
		return s.runJob(ctx, p.Name, "up", func(ctx context.Context) (any, *rpcError) {
			return s.ok(s.Service.ContainersUp(ctx, p.Name, service.UpOptions{
				Build: p.Build, ForceRecreate: p.ForceRecreate, RemoveOrphans: p.RemoveOrphans,
				Services: p.Services,
			}, stream))
		})

	case api.MethodContainersDown:
		var p struct {
			Name     string   `json:"name"`
			Volumes  bool     `json:"volumes"`
			Services []string `json:"services"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack, stream.command = p.Name, "down"
		return s.runJob(ctx, p.Name, "down", func(ctx context.Context) (any, *rpcError) {
			return s.ok(s.Service.ContainersDown(ctx, p.Name, service.DownOptions{Volumes: p.Volumes, Services: p.Services}, stream))
		})

	case api.MethodContainersRestart:
		var p struct {
			Name     string   `json:"name"`
			Services []string `json:"services"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack, stream.command = p.Name, "restart"
		return s.runJob(ctx, p.Name, "restart", func(ctx context.Context) (any, *rpcError) {
			return s.ok(s.Service.ContainersRestart(ctx, p.Name, p.Services, stream))
		})

	case api.MethodContainersBuild:
		var p struct {
			Name     string   `json:"name"`
			Services []string `json:"services"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack, stream.command = p.Name, "build"
		return s.runJob(ctx, p.Name, "build", func(ctx context.Context) (any, *rpcError) {
			return s.ok(s.Service.ContainersBuild(ctx, p.Name, p.Services, stream))
		})

	case api.MethodContainersPs:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		rows, err := s.Service.ContainersPs(ctx, p.Name)
		if err != nil {
			return nil, toRPCError(err)
		}
		out := make([]api.ContainerStatus, 0, len(rows))
		for _, row := range rows {
			out = append(out, api.ContainerStatus{Name: row.Name, Service: row.Service, State: row.State, Status: row.Status})
		}
		return out, nil

	case api.MethodContainersExec:
		var p struct {
			Name    string   `json:"name"`
			Service string   `json:"service"`
			Cmd     []string `json:"cmd"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		res, err := s.Service.ContainersExec(ctx, p.Name, p.Service, p.Cmd)
		if err != nil {
			return nil, toRPCError(err)
		}
		return api.ExecResult{ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr}, nil

	case api.MethodContainersLogs:
		var p struct {
			Name    string `json:"name"`
			Service string `json:"service"`
			Follow  bool   `json:"follow"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack, stream.command = p.Name, "logs"
		return s.ok(s.Service.ContainersLogs(ctx, p.Name, p.Service, p.Follow, stream))

	case api.MethodContainersNetworks:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		networks, err := s.Service.ContainersNetworks(ctx, p.Name)
		if err != nil {
			return nil, toRPCError(err)
		}
		out := make([]api.Network, 0, len(networks))
		for _, n := range networks {
			out = append(out, api.Network{Name: n.Name, Driver: n.Driver, Scope: n.Scope})
		}
		return out, nil

	case api.MethodContainersNetworkConnect, api.MethodContainersNetworkDisconnect:
		var p struct {
			Name     string   `json:"name"`
			Network  string   `json:"network"`
			Services []string `json:"services"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack, stream.command = p.Name, "network"
		return s.runJob(ctx, p.Name, "network", func(ctx context.Context) (any, *rpcError) {
			if method == api.MethodContainersNetworkConnect {
				return s.ok(s.Service.NetworkConnect(ctx, p.Name, p.Network, p.Services, stream))
			}
			return s.ok(s.Service.NetworkDisconnect(ctx, p.Name, p.Network, p.Services, stream))
		})

	case api.MethodCommandList:
		var p struct {
			Name string `json:"name"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		infos, err := s.Service.ListCommands(ctx, p.Name)
		if err != nil {
			return nil, toRPCError(err)
		}
		out := make([]api.CommandInfo, 0, len(infos))
		for _, info := range infos {
			ci := api.CommandInfo{Name: info.Name, Description: info.Description}
			for _, param := range info.Params {
				ci.Params = append(ci.Params, api.Param{
					Name: param.Name, Description: param.Description, Required: param.Required,
					Default: param.Default, Allowed: param.Allowed,
				})
			}
			out = append(out, ci)
		}
		return out, nil

	case api.MethodCommandRun:
		var p struct {
			Name    string            `json:"name"`
			Command string            `json:"command"`
			Params  map[string]string `json:"params"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		stream.stack, stream.command = p.Name, p.Command
		return s.runJob(ctx, p.Name, p.Command, func(ctx context.Context) (any, *rpcError) {
			if err := s.Service.RunCommand(ctx, p.Name, p.Command, p.Params, stream, stream.step); err != nil {
				return nil, toRPCError(err)
			}
			return api.CommandRunResult{OK: true, ExitCode: 0}, nil
		})
	}
	return nil, rpcErr(api.ErrInvalidParams, fmt.Sprintf("unknown method %q", method))
}

func (s *Server) ok(err error) (any, *rpcError) {
	if err != nil {
		return nil, toRPCError(err)
	}
	return api.OKResult{OK: true}, nil
}

func decodeParams(raw json.RawMessage, target any) *rpcError {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return rpcErr(api.ErrInvalidParams, "invalid params: "+err.Error())
	}
	return nil
}

func toRPCError(err error) *rpcError {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, stack.ErrNotFound):
		return rpcErr(api.ErrNotFound, err.Error())
	case errors.Is(err, git.ErrUnsafePull):
		return rpcErr(api.ErrConflict, "conflict; pull aborted: "+err.Error())
	default:
		return rpcErr(-32000, err.Error())
	}
}

func apiStack(st *stack.Stack) api.Stack {
	return api.Stack{
		StackSummary: aptStackSummary(st),
		ComposeFile:  st.ComposeFile,
		Allow:        append([]string(nil), st.Allow...),
		Exclude:      append([]string(nil), st.Exclude...),
	}
}

func aptStackSummary(st *stack.Stack) api.StackSummary {
	return api.StackSummary{
		ID: st.ID, Name: st.Name, Path: st.Path, Remote: st.Remote, Branch: st.Branch,
	}
}

// stream turns service output into JSON-RPC notifications.
type stream struct {
	conn    *serverConn
	stack   string
	command string
	method  string
}

// Write emits a stream.log notification. A disconnected client must not kill
// the job: the write error is swallowed and the command keeps running.
func (s *stream) Write(p []byte) (int, error) {
	if len(p) == 0 || s.conn == nil {
		return len(p), nil
	}
	params, _ := json.Marshal(api.StreamLog{
		Stack: s.stack, Command: s.command, Stream: "stdout", Data: string(p),
	})
	if err := s.conn.writer.writeJSON(api.Notification{
		JSONRPC: "2.0",
		Method:  api.NotifyStreamLog,
		Params:  params,
	}); err != nil {
		return len(p), nil
	}
	return len(p), nil
}

func (s *stream) step(step, total int, label string) {
	if s.conn == nil {
		return
	}
	params, _ := json.Marshal(api.StreamStep{
		Stack: s.stack, Command: s.command, Step: step, Total: total, Label: label,
	})
	_ = s.conn.writer.writeJSON(api.Notification{
		JSONRPC: "2.0",
		Method:  api.NotifyStreamStep,
		Params:  params,
	})
}
