package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/intel/aibox-orch/pkg/tier"
)

// FakeDriver is an in-memory Driver used by tests (and by stub tiers as a base).
// It models lifecycle transitions deterministically and can be told to fail a
// workload to exercise the reconciler's restart logic. It is safe for
// concurrent use.
type FakeDriver struct {
	mu     sync.Mutex
	tier   tier.Tier
	states map[string]Phase

	// readyAfter[name] = N means Status reports Pending for the first N calls
	// after Ensure, then Ready. Models probe warm-up. Default 0 => Ready at once.
	readyAfter map[string]int
	statusHits map[string]int

	// EnsureErr, if set for a name, makes Ensure return an error (simulating a
	// tier that cannot place the workload).
	ensureErr map[string]error
}

// NewFakeDriver returns a FakeDriver serving the given tier.
func NewFakeDriver(t tier.Tier) *FakeDriver {
	return &FakeDriver{
		tier:       t,
		states:     map[string]Phase{},
		readyAfter: map[string]int{},
		statusHits: map[string]int{},
		ensureErr:  map[string]error{},
	}
}

// SetReadyAfter configures how many Status polls return Pending before Ready.
func (f *FakeDriver) SetReadyAfter(name string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readyAfter[name] = n
}

// SetEnsureErr makes Ensure fail for a workload.
func (f *FakeDriver) SetEnsureErr(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureErr[name] = err
}

// Fail flips a running workload to Failed, simulating a crash.
func (f *FakeDriver) Fail(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[name] = PhaseFailed
}

// Tier implements Driver.
func (f *FakeDriver) Tier() tier.Tier { return f.tier }

// Ensure implements Driver.
func (f *FakeDriver) Ensure(_ context.Context, spec WorkloadSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ensureErr[spec.Name]; err != nil {
		return err
	}
	// (Re)starting resets the probe warm-up counter.
	if f.states[spec.Name] != PhaseReady {
		f.statusHits[spec.Name] = 0
		f.states[spec.Name] = PhasePending
	}
	return nil
}

// Stop implements Driver.
func (f *FakeDriver) Stop(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[name] = PhaseStopped
	return nil
}

// Remove implements Driver.
func (f *FakeDriver) Remove(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.states, name)
	delete(f.statusHits, name)
	return nil
}

// Status implements Driver.
func (f *FakeDriver) Status(_ context.Context, name string) (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ph, ok := f.states[name]
	if !ok {
		return State{Phase: PhaseUnknown}, nil
	}
	if ph == PhasePending {
		f.statusHits[name]++
		if f.statusHits[name] > f.readyAfter[name] {
			f.states[name] = PhaseReady
			ph = PhaseReady
		}
	}
	return State{Phase: ph}, nil
}

// Logs implements Driver.
func (f *FakeDriver) Logs(_ context.Context, name string, _ bool) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(fmt.Sprintf("fake logs for %s\n", name))), nil
}
