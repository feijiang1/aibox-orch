// Package runtime defines the tier Driver interface — the common lifecycle
// contract every isolation tier (container, native, kata, acrn) implements —
// plus the shared types the reconciler uses to drive workloads. The interface
// is deliberately CRI-flavored (Ensure/Stop/Status/Logs) so the containerd
// driver maps cleanly onto it and the VM tiers can be promoted later behind the
// same seam.
package runtime

import (
	"context"
	"io"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
	"github.com/intel/aibox-orch/pkg/tier"
)

// Phase is the observed lifecycle state of a workload, mirroring k8s pod phases
// closely enough to be familiar.
type Phase string

const (
	// PhaseUnknown means the driver has no record of the workload.
	PhaseUnknown Phase = "Unknown"
	// PhasePending means created/starting but not yet passing readiness.
	PhasePending Phase = "Pending"
	// PhaseReady means running and passing its readiness probe.
	PhaseReady Phase = "Ready"
	// PhaseFailed means the workload exited non-zero or crashed.
	PhaseFailed Phase = "Failed"
	// PhaseStopped means intentionally stopped.
	PhaseStopped Phase = "Stopped"
)

// State is a point-in-time status snapshot returned by a Driver.
type State struct {
	Phase   Phase
	Message string // human-readable detail (exit reason, probe failure, ...)
}

// WorkloadSpec is the resolved, tier-tagged unit handed to a Driver. It carries
// the original API workload plus the tier the placement logic resolved, so the
// driver does not re-derive it.
type WorkloadSpec struct {
	Name     string
	Tier     tier.Tier
	Workload v1.Workload
}

// Driver is the lifecycle contract for one isolation tier. Implementations must
// be safe for sequential calls from a single reconcile loop; Ensure must be
// idempotent (creating if absent, starting if stopped, no-op if already
// running).
type Driver interface {
	// Tier reports which tier this driver serves.
	Tier() tier.Tier
	// Ensure brings the workload to the running state (idempotent).
	Ensure(ctx context.Context, spec WorkloadSpec) error
	// Stop stops the workload but may retain its definition.
	Stop(ctx context.Context, name string) error
	// Remove stops and deletes all trace of the workload (idempotent).
	Remove(ctx context.Context, name string) error
	// Status returns the current observed state of the workload.
	Status(ctx context.Context, name string) (State, error)
	// Logs returns a reader streaming the workload's stdout/stderr.
	Logs(ctx context.Context, name string, follow bool) (io.ReadCloser, error)
}
