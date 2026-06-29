package reconcile

import (
	"context"
	"errors"
	"testing"
	"time"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

// fakeClock advances only when Sleep is called, so tests never wait on wall time.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) Sleep(d time.Duration) { c.t = c.t.Add(d) }

func bp(workloads ...v1.Workload) *v1.Blueprint {
	return &v1.Blueprint{
		APIVersion: v1.GroupVersion,
		Kind:       v1.Kind,
		Metadata:   v1.ObjectMeta{Name: "test"},
		Spec:       v1.BlueprintSpec{Workloads: workloads},
	}
}

func wl(name string, deps ...string) v1.Workload {
	return v1.Workload{
		Metadata: v1.ObjectMeta{Name: name},
		Spec: v1.WorkloadSpec{
			DependsOn:  deps,
			Containers: []v1.Container{{Name: name, Image: "img:latest"}},
		},
	}
}

func newTestReconciler() (*Reconciler, *runtime.FakeDriver) {
	fd := runtime.NewFakeDriver(tier.Container)
	r := New(map[tier.Tier]runtime.Driver{tier.Container: fd}, Options{
		PollInterval: 10 * time.Millisecond,
		ReadyTimeout: 5 * time.Second,
		Clock:        &fakeClock{t: time.Unix(0, 0)},
	})
	return r, fd
}

func TestUpBringsAllReady(t *testing.T) {
	r, fd := newTestReconciler()
	b := bp(wl("ima"), wl("openclaw", "ima"))
	fd.SetReadyAfter("ima", 2)      // pending for 2 polls then ready
	fd.SetReadyAfter("openclaw", 1)

	if err := r.Up(context.Background(), b); err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	states, _ := r.Status(context.Background(), b)
	for name, st := range states {
		if st.Phase != runtime.PhaseReady {
			t.Errorf("%s phase = %s, want Ready", name, st.Phase)
		}
	}
}

func TestUpFailsOnEnsureError(t *testing.T) {
	r, fd := newTestReconciler()
	b := bp(wl("x"))
	fd.SetEnsureErr("x", errors.New("boom"))
	if err := r.Up(context.Background(), b); err == nil {
		t.Fatal("expected Up() to fail when Ensure errors")
	}
}

func TestUpTimesOut(t *testing.T) {
	r, fd := newTestReconciler()
	b := bp(wl("slow"))
	fd.SetReadyAfter("slow", 1_000_000) // never ready within timeout
	err := r.Up(context.Background(), b)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestReconcileOnceRestartsFailed(t *testing.T) {
	r, fd := newTestReconciler()
	b := bp(wl("svc"))
	if err := r.Up(context.Background(), b); err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	fd.Fail("svc") // simulate crash

	acted, err := r.ReconcileOnce(context.Background(), b)
	if err != nil {
		t.Fatalf("ReconcileOnce() error: %v", err)
	}
	if len(acted) != 1 || acted[0] != "svc" {
		t.Fatalf("expected svc to be restarted, acted=%v", acted)
	}
}

func TestRestartPolicyNever(t *testing.T) {
	r, fd := newTestReconciler()
	w := wl("once")
	w.Spec.RestartPolicy = v1.RestartNever
	b := bp(w)
	if err := r.Up(context.Background(), b); err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	fd.Fail("once")
	acted, err := r.ReconcileOnce(context.Background(), b)
	if err != nil {
		t.Fatalf("ReconcileOnce() error: %v", err)
	}
	if len(acted) != 0 {
		t.Fatalf("RestartNever workload should not restart, acted=%v", acted)
	}
}

func TestRestartSingleWorkload(t *testing.T) {
	r, _ := newTestReconciler()
	b := bp(wl("a"), wl("b"))
	if err := r.Up(context.Background(), b); err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	if err := r.Restart(context.Background(), b, "a"); err != nil {
		t.Fatalf("Restart() error: %v", err)
	}
	if err := r.Restart(context.Background(), b, "ghost"); err == nil {
		t.Fatal("expected error restarting unknown workload")
	}
}

func TestNoDriverForTier(t *testing.T) {
	// Reconciler with only a container driver, workload wants kata.
	fd := runtime.NewFakeDriver(tier.Container)
	r := New(map[tier.Tier]runtime.Driver{tier.Container: fd}, Options{Clock: &fakeClock{}})
	w := wl("sandbox")
	w.Spec.RuntimeClassName = "kata"
	if err := r.Up(context.Background(), bp(w)); err == nil {
		t.Fatal("expected error when no driver registered for kata tier")
	}
}
