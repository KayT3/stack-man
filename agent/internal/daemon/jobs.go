package daemon

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/example/stack-man/pkg/api"
)

// job is a mutating operation that keeps running after the client that started
// it disconnects. Its context is rooted at the daemon, not a connection.
type job struct {
	id      string
	stack   string
	command string
	started time.Time
	cancel  context.CancelFunc
}

var jobSeq atomic.Uint64

// detached reports whether method runs as a job. Streaming reads (logs) and
// queries stay tied to the connection; mutations outlive it.
func detached(method string) bool {
	return api.Mutating(method)
}

// baseContext is the daemon lifetime context, falling back to Background when
// the server is driven without Run (tests).
func (s *Server) baseContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runCtx != nil {
		return s.runCtx
	}
	return context.Background()
}

// beginJob registers a running operation for stack. Only one mutating job per
// stack may run at a time, so two builds cannot race.
func (s *Server) beginJob(ctx context.Context, stackName, command string) (context.Context, *job, error) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if existing, ok := s.jobs[stackName]; ok {
		return nil, nil, fmt.Errorf("%s is already running on stack %q (started %s); wait for it to finish",
			existing.command, stackName, time.Since(existing.started).Round(time.Second))
	}
	jobCtx, cancel := context.WithCancel(ctx)
	j := &job{
		id:      fmt.Sprintf("job-%d-%d", jobSeq.Add(1), time.Now().UnixNano()),
		stack:   stackName,
		command: command,
		started: time.Now(),
		cancel:  cancel,
	}
	s.jobs[stackName] = j
	return jobCtx, j, nil
}

func (s *Server) endJob(j *job) {
	j.cancel()
	s.jobsMu.Lock()
	if current, ok := s.jobs[j.stack]; ok && current == j {
		delete(s.jobs, j.stack)
	}
	s.jobsMu.Unlock()
}

// runJob runs fn as a tracked job. fn receives a context that is not cancelled
// when the client connection drops.
func (s *Server) runJob(ctx context.Context, stackName, command string, fn func(context.Context) (any, *rpcError)) (any, *rpcError) {
	jobCtx, j, err := s.beginJob(ctx, stackName, command)
	if err != nil {
		return nil, rpcErr(api.ErrConflict, err.Error())
	}
	defer s.endJob(j)
	return fn(jobCtx)
}

// listJobs snapshots the operations currently running on this agent.
func (s *Server) listJobs() []api.Job {
	now := time.Now()
	s.jobsMu.Lock()
	out := make([]api.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, api.Job{
			ID:             j.id,
			Stack:          j.stack,
			Command:        j.command,
			Started:        j.started.UTC().Format(time.RFC3339),
			ElapsedSeconds: int(now.Sub(j.started).Seconds()),
		})
	}
	s.jobsMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Stack == out[j].Stack {
			return out[i].Started < out[j].Started
		}
		return out[i].Stack < out[j].Stack
	})
	return out
}
