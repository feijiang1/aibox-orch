// Package containerd is the real container-tier driver. It talks to a running
// containerd daemon over its gRPC socket using the containerd Go client —
// exactly the substrate k3s uses — and maps each workload to a containerd
// container + task. The isolation tier's RuntimeClass selects the runtime
// handler (default runc here; kata/acrn live in their own packages).
//
// Resource limits derived by pkg/quota are applied as OCI/cgroup constraints.
package containerd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/intel/aibox-orch/pkg/quota"
	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

// Namespace is the containerd namespace all AI Box workloads live in.
const Namespace = "aibox"

// Driver implements runtime.Driver against a containerd daemon.
type Driver struct {
	client *containerd.Client
	ns     string
}

// New connects to containerd at the given socket address (e.g.
// "/run/containerd/containerd.sock") and returns a container-tier driver.
func New(address string) (*Driver, error) {
	if address == "" {
		address = "/run/containerd/containerd.sock"
	}
	c, err := containerd.New(address)
	if err != nil {
		return nil, fmt.Errorf("connect containerd at %s: %w", address, err)
	}
	return &Driver{client: c, ns: Namespace}, nil
}

// Close releases the containerd client.
func (d *Driver) Close() error { return d.client.Close() }

// Tier implements runtime.Driver.
func (d *Driver) Tier() tier.Tier { return tier.Container }

func (d *Driver) ctx(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, d.ns)
}

// Ensure pulls the image if needed, creates the container with resource limits,
// and starts its task. Idempotent: if the task already runs it is a no-op.
func (d *Driver) Ensure(ctx context.Context, spec runtime.WorkloadSpec) error {
	ctx = d.ctx(ctx)
	if len(spec.Workload.Spec.Containers) == 0 {
		return fmt.Errorf("workload %q has no containers", spec.Name)
	}
	c := spec.Workload.Spec.Containers[0]

	// Already running?
	if st, _ := d.Status(ctx, spec.Name); st.Phase == runtime.PhaseReady || st.Phase == runtime.PhasePending {
		return nil
	}

	image, err := d.client.Pull(ctx, c.Image, containerd.WithPullUnpack)
	if err != nil {
		return fmt.Errorf("pull %s: %w", c.Image, err)
	}

	limits, err := quota.FromContainer(c)
	if err != nil {
		return err
	}
	specOpts := []oci.SpecOpts{oci.WithImageConfig(image)}
	if len(c.Command) > 0 || len(c.Args) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(append(append([]string{}, c.Command...), c.Args...)...))
	}
	for _, e := range c.Env {
		specOpts = append(specOpts, oci.WithEnv([]string{e.Name + "=" + e.Value}))
	}
	specOpts = append(specOpts, withLimits(limits)...)

	// Remove any stale container with the same id first.
	_ = d.Remove(ctx, spec.Name)

	cont, err := d.client.NewContainer(ctx, spec.Name,
		containerd.WithNewSnapshot(spec.Name+"-snap", image),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		return fmt.Errorf("create container %q: %w", spec.Name, err)
	}

	task, err := cont.NewTask(ctx, cio.NullIO)
	if err != nil {
		return fmt.Errorf("create task %q: %w", spec.Name, err)
	}
	if err := task.Start(ctx); err != nil {
		return fmt.Errorf("start task %q: %w", spec.Name, err)
	}
	return nil
}

// withLimits maps normalized quota.Limits onto OCI cgroup constraints.
func withLimits(l quota.Limits) []oci.SpecOpts {
	var opts []oci.SpecOpts
	if l.MemoryBytes > 0 {
		opts = append(opts, oci.WithMemoryLimit(uint64(l.MemoryBytes)))
	}
	if l.CPUMillis > 0 {
		// CFS quota: period 100000us, quota = millis/1000 * period.
		quotaUS := l.CPUMillis * 100
		period := uint64(100000)
		q := quotaUS
		opts = append(opts, oci.WithCPUCFS(q, period))
	}
	// NPU/GPU device passthrough would be added here as device specs on real HW.
	_ = specs.LinuxDeviceCgroup{}
	return opts
}

// Stop kills the task but keeps the container definition.
func (d *Driver) Stop(ctx context.Context, name string) error {
	ctx = d.ctx(ctx)
	cont, err := d.client.LoadContainer(ctx, name)
	if err != nil {
		return nil // nothing to stop
	}
	task, err := cont.Task(ctx, nil)
	if err != nil {
		return nil
	}
	_ = task.Kill(ctx, syscall.SIGTERM)
	// Best-effort wait, then force.
	select {
	case <-waitExit(ctx, task):
	case <-time.After(3 * time.Second):
		_ = task.Kill(ctx, syscall.SIGKILL)
	}
	_, err = task.Delete(ctx)
	return err
}

// Remove stops the task and deletes the container and snapshot.
func (d *Driver) Remove(ctx context.Context, name string) error {
	ctx = d.ctx(ctx)
	cont, err := d.client.LoadContainer(ctx, name)
	if err != nil {
		return nil
	}
	if task, err := cont.Task(ctx, nil); err == nil {
		_ = task.Kill(ctx, syscall.SIGKILL)
		select {
		case <-waitExit(ctx, task):
		case <-time.After(3 * time.Second):
		}
		_, _ = task.Delete(ctx)
	}
	return cont.Delete(ctx, containerd.WithSnapshotCleanup)
}

// Status maps the containerd task status to a runtime.Phase.
func (d *Driver) Status(ctx context.Context, name string) (runtime.State, error) {
	ctx = d.ctx(ctx)
	cont, err := d.client.LoadContainer(ctx, name)
	if err != nil {
		return runtime.State{Phase: runtime.PhaseUnknown}, nil
	}
	task, err := cont.Task(ctx, nil)
	if err != nil {
		return runtime.State{Phase: runtime.PhaseStopped}, nil
	}
	status, err := task.Status(ctx)
	if err != nil {
		return runtime.State{Phase: runtime.PhaseUnknown, Message: err.Error()}, nil
	}
	switch status.Status {
	case containerd.Running:
		return runtime.State{Phase: runtime.PhaseReady}, nil
	case containerd.Created, containerd.Paused, containerd.Pausing:
		return runtime.State{Phase: runtime.PhasePending}, nil
	case containerd.Stopped:
		if status.ExitStatus != 0 {
			return runtime.State{Phase: runtime.PhaseFailed, Message: fmt.Sprintf("exit %d", status.ExitStatus)}, nil
		}
		return runtime.State{Phase: runtime.PhaseStopped}, nil
	default:
		return runtime.State{Phase: runtime.PhaseUnknown, Message: string(status.Status)}, nil
	}
}

// Logs is best-effort for the MVP (containerd tasks need explicit IO wiring at
// creation to capture logs; NullIO is used above). Returns an explanatory note.
func (d *Driver) Logs(_ context.Context, name string, _ bool) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(
		"containerd driver: per-task log capture not enabled in MVP for " + name + "\n")), nil
}

func waitExit(ctx context.Context, task containerd.Task) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		statusC, err := task.Wait(ctx)
		if err != nil {
			return
		}
		<-statusC
	}()
	return ch
}
