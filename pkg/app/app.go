// Package app wires the tier drivers and the reconciler into a single object
// the CLI drives. It centralizes driver registration so the container tier can
// fall back to a simulated driver when no containerd daemon is reachable (dev
// boxes), while the real containerd driver is used when it is.
package app

import (
	"log/slog"
	"time"

	cddriver "github.com/intel/aibox-orch/pkg/runtime/containerd"
	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/runtime/acrn"
	"github.com/intel/aibox-orch/pkg/runtime/kata"
	"github.com/intel/aibox-orch/pkg/runtime/native"
	"github.com/intel/aibox-orch/pkg/reconcile"
	"github.com/intel/aibox-orch/pkg/tier"
)

// Config controls how the app builds its drivers.
type Config struct {
	// ContainerdAddress is the containerd socket. If empty, the default is used.
	ContainerdAddress string
	// SimulateContainer forces the simulated container driver (no daemon needed).
	SimulateContainer bool
	// Logger is the structured logger; nil uses slog.Default().
	Logger *slog.Logger
	// PollInterval / ReadyTimeout tune the reconciler.
	PollInterval time.Duration
	ReadyTimeout time.Duration
}

// App bundles the reconciler with the constructed drivers.
type App struct {
	Reconciler *reconcile.Reconciler
	drivers    map[tier.Tier]runtime.Driver
	closers    []func() error
}

// New builds an App. The container tier uses the real containerd driver unless
// SimulateContainer is set or connecting fails, in which case it falls back to
// a simulated driver so the CLI still works on a plain dev box.
func New(cfg Config) (*App, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	drivers := map[tier.Tier]runtime.Driver{
		tier.Native: native.New(),
		tier.Kata:   kata.New(log),
		tier.ACRNVM: acrn.New(log),
	}
	var closers []func() error

	if cfg.SimulateContainer {
		log.Warn("container tier: using simulated driver (SimulateContainer=true)")
		drivers[tier.Container] = runtime.NewFakeDriver(tier.Container)
	} else if cd, err := cddriver.New(cfg.ContainerdAddress); err != nil {
		log.Warn("container tier: containerd unreachable, falling back to simulated driver", "error", err)
		drivers[tier.Container] = runtime.NewFakeDriver(tier.Container)
	} else {
		log.Info("container tier: connected to containerd", "address", cfg.ContainerdAddress)
		drivers[tier.Container] = cd
		closers = append(closers, cd.Close)
	}

	r := reconcile.New(drivers, reconcile.Options{
		PollInterval: cfg.PollInterval,
		ReadyTimeout: cfg.ReadyTimeout,
	})
	return &App{Reconciler: r, drivers: drivers, closers: closers}, nil
}

// reconcileInterval is the default self-heal sweep interval used by `aibox run`.
const reconcileInterval = 3 * time.Second

// Driver returns the driver registered for a tier (for logs/exec on a workload).
func (a *App) Driver(t tier.Tier) (runtime.Driver, bool) {
	d, ok := a.drivers[t]
	return d, ok
}

// Close releases any driver resources (e.g. the containerd client).
func (a *App) Close() error {
	var firstErr error
	for _, c := range a.closers {
		if err := c(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ReconcileInterval exposes the default self-heal sweep interval.
func (a *App) ReconcileInterval() time.Duration { return reconcileInterval }
