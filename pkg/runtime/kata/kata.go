// Package kata is the KATA-isolation tier driver. KATA runs each workload in a
// lightweight VM, which is how untrusted code is isolated (FR-2). On real
// hardware this maps to containerd's io.containerd.kata.v2 runtime handler.
//
// In the MVP this is a STUB: it satisfies the runtime.Driver contract and
// simulates the VM lifecycle in memory so `aibox up` works end-to-end on a
// plain dev box, while clearly logging that a real deployment would route to
// the Kata shim. It sits at the correct seam to be promoted to a real driver.
package kata

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

// Driver is the stub KATA driver.
type Driver struct {
	sim *runtime.FakeDriver
	log *slog.Logger
}

// New returns a KATA stub driver. logger may be nil.
func New(logger *slog.Logger) *Driver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Driver{sim: runtime.NewFakeDriver(tier.Kata), log: logger}
}

// Tier implements runtime.Driver.
func (d *Driver) Tier() tier.Tier { return tier.Kata }

// Ensure implements runtime.Driver.
func (d *Driver) Ensure(ctx context.Context, spec runtime.WorkloadSpec) error {
	d.log.Info("KATA stub: would launch workload in a Kata VM (io.containerd.kata.v2)",
		"workload", spec.Name, "image", firstImage(spec))
	return d.sim.Ensure(ctx, spec)
}

// Stop implements runtime.Driver.
func (d *Driver) Stop(ctx context.Context, name string) error { return d.sim.Stop(ctx, name) }

// Remove implements runtime.Driver.
func (d *Driver) Remove(ctx context.Context, name string) error { return d.sim.Remove(ctx, name) }

// Status implements runtime.Driver.
func (d *Driver) Status(ctx context.Context, name string) (runtime.State, error) {
	return d.sim.Status(ctx, name)
}

// Logs implements runtime.Driver.
func (d *Driver) Logs(ctx context.Context, name string, follow bool) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("kata-vm stub logs for " + name + "\n")), nil
}

func firstImage(spec runtime.WorkloadSpec) string {
	if len(spec.Workload.Spec.Containers) > 0 {
		return spec.Workload.Spec.Containers[0].Image
	}
	return ""
}
