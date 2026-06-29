// Package acrn is the ACRN Secure VM tier driver. ACRN provides hardware-backed
// VM isolation for security-sensitive tenants (MS3 enforcement, credential
// vault, closed-weight high-value models per the platform HLD). On real
// hardware this maps to the ACRN hypervisor, typically via a Kata hypervisor
// backend or a dedicated VM manager.
//
// In the MVP this is a STUB: it satisfies runtime.Driver and simulates the
// Secure VM lifecycle in memory, logging that a real deployment would create an
// ACRN Secure VM. It sits at the correct seam for later promotion.
package acrn

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

// Driver is the stub ACRN Secure VM driver.
type Driver struct {
	sim *runtime.FakeDriver
	log *slog.Logger
}

// New returns an ACRN stub driver. logger may be nil.
func New(logger *slog.Logger) *Driver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Driver{sim: runtime.NewFakeDriver(tier.ACRNVM), log: logger}
}

// Tier implements runtime.Driver.
func (d *Driver) Tier() tier.Tier { return tier.ACRNVM }

// Ensure implements runtime.Driver.
func (d *Driver) Ensure(ctx context.Context, spec runtime.WorkloadSpec) error {
	d.log.Info("ACRN stub: would provision a Secure VM (hypervisor passthrough)",
		"workload", spec.Name)
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
	return io.NopCloser(strings.NewReader("acrn-secure-vm stub logs for " + name + "\n")), nil
}
