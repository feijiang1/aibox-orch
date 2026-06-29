package app

import (
	"context"
	"testing"
	"time"

	"github.com/intel/aibox-orch/pkg/blueprint"
	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

const homeBlueprint = `
apiVersion: aibox.io/v1
kind: Blueprint
metadata: { name: home }
spec:
  workloads:
    - metadata: { name: ima }
      spec:
        containers: [{ name: ima, image: busybox, command: ["sleep","1"] }]
    - metadata: { name: openclaw }
      spec:
        dependsOn: [ima]
        containers: [{ name: openclaw, image: busybox, command: ["sleep","1"] }]
    - metadata: { name: sandbox }
      spec:
        runtimeClassName: kata
        containers: [{ name: s, image: busybox, command: ["sleep","1"] }]
    - metadata: { name: vault }
      spec:
        runtimeClassName: acrn
        containers: [{ name: v, image: busybox, command: ["sleep","1"] }]
`

// TestAppUpAllTiersSimulated brings the full multi-tier stack to Ready using the
// simulated container driver (no containerd needed) and asserts cold-start is
// well under the 60s NFR.
func TestAppUpAllTiersSimulated(t *testing.T) {
	bp, err := blueprint.Parse([]byte(homeBlueprint))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a, err := New(Config{
		SimulateContainer: true,
		PollInterval:      time.Millisecond,
		ReadyTimeout:      10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	start := time.Now()
	if err := a.Reconciler.Up(context.Background(), bp); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if d := time.Since(start); d > 60*time.Second {
		t.Errorf("cold start took %s, exceeds 60s NFR", d)
	}

	states, err := a.Reconciler.Status(context.Background(), bp)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(states) != 4 {
		t.Fatalf("expected 4 workloads, got %d", len(states))
	}
	for name, st := range states {
		if st.Phase != runtime.PhaseReady {
			t.Errorf("workload %s phase = %s, want Ready", name, st.Phase)
		}
	}
}

// TestAppFallsBackWhenContainerdUnreachable verifies the container tier falls
// back to the simulated driver rather than failing when no daemon is present.
func TestAppFallsBackWhenContainerdUnreachable(t *testing.T) {
	a, err := New(Config{ContainerdAddress: "/nonexistent/containerd.sock"})
	if err != nil {
		t.Fatalf("New should not fail on unreachable containerd: %v", err)
	}
	defer a.Close()
	if _, ok := a.Driver(tier.Container); !ok {
		t.Fatal("container driver should be registered (simulated fallback)")
	}
}
