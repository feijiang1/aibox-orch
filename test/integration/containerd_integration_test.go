//go:build integration

// Package integration holds tests that require a running containerd daemon.
// They are excluded from normal `go test ./...` by the `integration` build tag.
//
// Run them with a reachable containerd socket:
//
//	sudo $(which containerd) &                 # or use the system containerd
//	go test -tags integration ./test/integration/ \
//	    -run TestFullStack -v \
//	    -args -containerd=/run/containerd/containerd.sock
//
// The test exercises the real containerd driver end-to-end: it brings up a
// multi-workload blueprint, asserts every workload reaches Ready within the 60s
// cold-start NFR, restarts one workload and asserts the <5s NFR, then tears
// everything down.
package integration

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/intel/aibox-orch/pkg/app"
	"github.com/intel/aibox-orch/pkg/blueprint"
	"github.com/intel/aibox-orch/pkg/runtime"
)

var containerdAddr = flag.String("containerd", "/run/containerd/containerd.sock", "containerd socket for integration test")

// A minimal blueprint using a tiny public image on the container tier plus the
// stub kata/acrn tiers, so the full placement path is exercised.
const integrationBlueprint = `
apiVersion: aibox.io/v1
kind: Blueprint
metadata: { name: itest }
spec:
  workloads:
    - metadata: { name: base }
      spec:
        containers:
          - name: base
            image: docker.io/library/busybox:latest
            command: ["sleep", "300"]
            resources:
              limits: { cpu: "1", memory: 256Mi }
    - metadata: { name: dependent }
      spec:
        dependsOn: [base]
        containers:
          - name: dependent
            image: docker.io/library/busybox:latest
            command: ["sleep", "300"]
    - metadata: { name: sandbox }
      spec:
        runtimeClassName: kata
        containers:
          - name: s
            image: docker.io/library/busybox:latest
            command: ["sleep", "300"]
`

func TestFullStack(t *testing.T) {
	if _, err := os.Stat(*containerdAddr); err != nil {
		t.Skipf("containerd socket %s not available: %v", *containerdAddr, err)
	}

	bp, err := blueprint.Parse([]byte(integrationBlueprint))
	if err != nil {
		t.Fatalf("parse blueprint: %v", err)
	}

	a, err := app.New(app.Config{
		ContainerdAddress: *containerdAddr,
		PollInterval:      500 * time.Millisecond,
		ReadyTimeout:      60 * time.Second,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	defer a.Close()

	ctx := context.Background()
	t.Cleanup(func() { _ = a.Reconciler.Down(ctx, bp) })

	// FR-1 + NFR cold-start < 60s.
	start := time.Now()
	if err := a.Reconciler.Up(ctx, bp); err != nil {
		t.Fatalf("Up: %v", err)
	}
	coldStart := time.Since(start)
	t.Logf("cold start: %s", coldStart)
	if coldStart > 60*time.Second {
		t.Errorf("cold start %s exceeds 60s NFR", coldStart)
	}

	states, err := a.Reconciler.Status(ctx, bp)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for name, st := range states {
		if st.Phase != runtime.PhaseReady {
			t.Errorf("workload %s phase = %s, want Ready", name, st.Phase)
		}
	}

	// NFR restart < 5s, isolated to one workload.
	rs := time.Now()
	if err := a.Reconciler.Restart(ctx, bp, "base"); err != nil {
		t.Fatalf("Restart base: %v", err)
	}
	restart := time.Since(rs)
	t.Logf("restart base: %s", restart)
	if restart > 5*time.Second {
		t.Errorf("restart %s exceeds 5s NFR", restart)
	}

	// Other workloads should remain Ready (isolation).
	if st, _ := a.Reconciler.Status(ctx, bp); st["dependent"].Phase != runtime.PhaseReady {
		t.Errorf("dependent should stay Ready during base restart, got %s", st["dependent"].Phase)
	}
}
