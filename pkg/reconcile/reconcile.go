// Package reconcile implements the k3s/k8s-style control loop that drives a
// blueprint's desired state to reality: it places each workload on its tier's
// Driver, brings workloads up in dependency order, waits for readiness, and
// (level-triggered) restarts any workload that drifts to Failed.
package reconcile

import (
	"context"
	"fmt"
	"sort"
	"time"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
	"github.com/intel/aibox-orch/pkg/blueprint"
	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/telemetry"
	"github.com/intel/aibox-orch/pkg/tier"
)

// Clock abstracts time so tests can run deterministically without real sleeps.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time  { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// Options tunes the reconciler timing.
type Options struct {
	// PollInterval is how often Status is polled while waiting for readiness.
	PollInterval time.Duration
	// ReadyTimeout bounds how long a single workload may take to become Ready.
	ReadyTimeout time.Duration
	// Clock is injectable for tests; nil uses the real clock.
	Clock Clock
}

func (o *Options) withDefaults() {
	if o.PollInterval <= 0 {
		o.PollInterval = 500 * time.Millisecond
	}
	if o.ReadyTimeout <= 0 {
		o.ReadyTimeout = 60 * time.Second
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
}

// Reconciler owns the drivers and reconciles blueprints against them.
type Reconciler struct {
	drivers map[tier.Tier]runtime.Driver
	opts    Options
}

// New builds a Reconciler from a set of tier drivers and options.
func New(drivers map[tier.Tier]runtime.Driver, opts Options) *Reconciler {
	opts.withDefaults()
	return &Reconciler{drivers: drivers, opts: opts}
}

// resolve builds the per-workload driver spec, erroring if no driver is
// registered for the workload's tier.
func (r *Reconciler) resolve(w v1.Workload) (runtime.Driver, runtime.WorkloadSpec, error) {
	t, err := tier.Resolve(w)
	if err != nil {
		return nil, runtime.WorkloadSpec{}, err
	}
	d, ok := r.drivers[t]
	if !ok {
		return nil, runtime.WorkloadSpec{}, fmt.Errorf("no driver registered for tier %q (workload %q)", t, w.Metadata.Name)
	}
	return d, runtime.WorkloadSpec{Name: w.Metadata.Name, Tier: t, Workload: w}, nil
}

// Up brings the whole blueprint to Ready in dependency order. It returns once
// every workload is Ready, or an error if any workload fails or times out.
// This is the single-manifest bring-up behind `aibox up` (FR-1).
func (r *Reconciler) Up(ctx context.Context, bp *v1.Blueprint) error {
	ctx, span := telemetry.StartSpan(ctx, "reconcile.Up", "blueprint", bp.Metadata.Name)
	defer span.End()
	byName := make(map[string]v1.Workload, len(bp.Spec.Workloads))
	for _, w := range bp.Spec.Workloads {
		byName[w.Metadata.Name] = w
	}
	for _, name := range blueprint.StartOrder(bp) {
		w := byName[name]
		d, spec, err := r.resolve(w)
		if err != nil {
			return err
		}
		wctx, wspan := telemetry.StartSpan(ctx, "reconcile.workload", "workload", name, "tier", string(spec.Tier))
		if err := d.Ensure(wctx, spec); err != nil {
			wspan.End()
			return fmt.Errorf("ensure %q: %w", name, err)
		}
		if err := r.waitReady(wctx, d, name); err != nil {
			wspan.End()
			return err
		}
		wspan.End()
	}
	return nil
}

// waitReady polls Status until the workload is Ready, the context is cancelled,
// the workload Fails, or ReadyTimeout elapses.
func (r *Reconciler) waitReady(ctx context.Context, d runtime.Driver, name string) error {
	deadline := r.opts.Clock.Now().Add(r.opts.ReadyTimeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st, err := d.Status(ctx, name)
		if err != nil {
			return fmt.Errorf("status %q: %w", name, err)
		}
		switch st.Phase {
		case runtime.PhaseReady:
			return nil
		case runtime.PhaseFailed:
			return fmt.Errorf("workload %q failed: %s", name, st.Message)
		}
		if !r.opts.Clock.Now().Before(deadline) {
			return fmt.Errorf("workload %q not ready within %s (phase=%s)", name, r.opts.ReadyTimeout, st.Phase)
		}
		r.opts.Clock.Sleep(r.opts.PollInterval)
	}
}

// Down stops and removes all workloads in reverse dependency order so that
// dependents are torn down before their dependencies.
func (r *Reconciler) Down(ctx context.Context, bp *v1.Blueprint) error {
	order := blueprint.StartOrder(bp)
	byName := make(map[string]v1.Workload, len(bp.Spec.Workloads))
	for _, w := range bp.Spec.Workloads {
		byName[w.Metadata.Name] = w
	}
	var firstErr error
	for i := len(order) - 1; i >= 0; i-- {
		w := byName[order[i]]
		d, _, err := r.resolve(w)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := d.Remove(ctx, w.Metadata.Name); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Restart restarts a single workload in place (FR / NFR: < 5s, isolated). It
// does not touch other workloads.
func (r *Reconciler) Restart(ctx context.Context, bp *v1.Blueprint, name string) error {
	w, ok := find(bp, name)
	if !ok {
		return fmt.Errorf("workload %q not found in blueprint %q", name, bp.Metadata.Name)
	}
	d, spec, err := r.resolve(w)
	if err != nil {
		return err
	}
	if err := d.Stop(ctx, name); err != nil {
		return fmt.Errorf("stop %q: %w", name, err)
	}
	if err := d.Ensure(ctx, spec); err != nil {
		return fmt.Errorf("ensure %q: %w", name, err)
	}
	return r.waitReady(ctx, d, name)
}

// ReconcileOnce performs one level-triggered sweep: any workload that should be
// running but is Failed/Stopped/Unknown is re-Ensured (subject to its restart
// policy). It returns the names it acted on. This is the loop body a supervisor
// calls periodically for self-healing (NFR auto-restart).
func (r *Reconciler) ReconcileOnce(ctx context.Context, bp *v1.Blueprint) ([]string, error) {
	var acted []string
	for _, w := range bp.Spec.Workloads {
		d, spec, err := r.resolve(w)
		if err != nil {
			return acted, err
		}
		st, err := d.Status(ctx, w.Metadata.Name)
		if err != nil {
			return acted, fmt.Errorf("status %q: %w", w.Metadata.Name, err)
		}
		if shouldRestart(w.Spec.RestartPolicy, st.Phase) {
			if err := d.Ensure(ctx, spec); err != nil {
				return acted, fmt.Errorf("restart %q: %w", w.Metadata.Name, err)
			}
			acted = append(acted, w.Metadata.Name)
		}
	}
	sort.Strings(acted)
	return acted, nil
}

// Run executes ReconcileOnce on a ticker until the context is cancelled,
// providing the continuous self-healing loop behind a running orchestrator.
func (r *Reconciler) Run(ctx context.Context, bp *v1.Blueprint, interval time.Duration) error {
	for {
		if _, err := r.ReconcileOnce(ctx, bp); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			r.opts.Clock.Sleep(interval)
		}
	}
}

// shouldRestart applies restart-policy semantics to an observed phase.
func shouldRestart(policy v1.RestartPolicy, ph runtime.Phase) bool {
	if policy == "" {
		policy = v1.RestartAlways
	}
	switch policy {
	case v1.RestartNever:
		return false
	case v1.RestartOnFailure:
		return ph == runtime.PhaseFailed
	default: // Always
		return ph == runtime.PhaseFailed || ph == runtime.PhaseStopped || ph == runtime.PhaseUnknown
	}
}

func find(bp *v1.Blueprint, name string) (v1.Workload, bool) {
	for _, w := range bp.Spec.Workloads {
		if w.Metadata.Name == name {
			return w, true
		}
	}
	return v1.Workload{}, false
}

// Status returns the current phase of every workload in the blueprint, keyed by
// name, for `aibox ps`/`status`.
func (r *Reconciler) Status(ctx context.Context, bp *v1.Blueprint) (map[string]runtime.State, error) {
	out := make(map[string]runtime.State, len(bp.Spec.Workloads))
	for _, w := range bp.Spec.Workloads {
		d, _, err := r.resolve(w)
		if err != nil {
			return nil, err
		}
		st, err := d.Status(ctx, w.Metadata.Name)
		if err != nil {
			return nil, err
		}
		out[w.Metadata.Name] = st
	}
	return out, nil
}
