// Package native is the native-process tier driver: it runs a workload directly
// as a host OS process (no container or VM). This is a real driver — it uses
// os/exec to launch and supervise processes, captures their output to a ring
// buffer, and reports liveness as the workload phase.
//
// The native tier suits trusted, host-coupled components that need no isolation
// (e.g. a local device shim). Readiness is process-liveness for the MVP; richer
// probes can be layered on later.
package native

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

type proc struct {
	cmd  *exec.Cmd
	out  *ringBuffer
	mu   sync.Mutex
	done bool
	err  error
}

// Driver runs workloads as native host processes.
type Driver struct {
	mu    sync.Mutex
	procs map[string]*proc
}

// New returns a native driver.
func New() *Driver {
	return &Driver{procs: map[string]*proc{}}
}

// Tier implements runtime.Driver.
func (d *Driver) Tier() tier.Tier { return tier.Native }

// Ensure starts the workload's first container command as a host process if not
// already running. Idempotent.
func (d *Driver) Ensure(ctx context.Context, spec runtime.WorkloadSpec) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if p, ok := d.procs[spec.Name]; ok && !p.finished() {
		return nil // already running
	}

	containers := spec.Workload.Spec.Containers
	if len(containers) == 0 {
		return fmt.Errorf("native workload %q has no containers", spec.Name)
	}
	c := containers[0]
	argv := append(append([]string{}, c.Command...), c.Args...)
	if len(argv) == 0 {
		return fmt.Errorf("native workload %q: container %q has no command to exec", spec.Name, c.Name)
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	for _, e := range c.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	buf := newRingBuffer(64 * 1024)
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("native start %q: %w", spec.Name, err)
	}
	p := &proc{cmd: cmd, out: buf}
	d.procs[spec.Name] = p
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.done = true
		p.err = err
		p.mu.Unlock()
	}()
	return nil
}

// Stop sends a kill to the process.
func (d *Driver) Stop(_ context.Context, name string) error {
	d.mu.Lock()
	p, ok := d.procs[name]
	d.mu.Unlock()
	if !ok {
		return nil
	}
	if p.cmd.Process != nil && !p.finished() {
		return p.cmd.Process.Kill()
	}
	return nil
}

// Remove stops and forgets the process.
func (d *Driver) Remove(ctx context.Context, name string) error {
	if err := d.Stop(ctx, name); err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.procs, name)
	d.mu.Unlock()
	return nil
}

// Status reports Ready while the process is alive, Failed if it exited non-zero,
// Stopped if it exited cleanly, Unknown if never started.
func (d *Driver) Status(_ context.Context, name string) (runtime.State, error) {
	d.mu.Lock()
	p, ok := d.procs[name]
	d.mu.Unlock()
	if !ok {
		return runtime.State{Phase: runtime.PhaseUnknown}, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		return runtime.State{Phase: runtime.PhaseReady}, nil
	}
	if p.err != nil {
		return runtime.State{Phase: runtime.PhaseFailed, Message: p.err.Error()}, nil
	}
	return runtime.State{Phase: runtime.PhaseStopped}, nil
}

// Logs returns the captured output. follow is best-effort (returns a snapshot).
func (d *Driver) Logs(_ context.Context, name string, _ bool) (io.ReadCloser, error) {
	d.mu.Lock()
	p, ok := d.procs[name]
	d.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("native workload %q not found", name)
	}
	return io.NopCloser(bytes.NewReader(p.out.Bytes())), nil
}

func (p *proc) finished() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// ringBuffer is a fixed-size circular byte buffer for capturing recent output.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	full bool
	pos  int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size), size: size}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		r.buf[r.pos] = b
		r.pos = (r.pos + 1) % r.size
		if r.pos == 0 {
			r.full = true
		}
	}
	return len(p), nil
}

func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]byte(nil), r.buf[:r.pos]...)
	}
	out := make([]byte, 0, r.size)
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	return out
}
