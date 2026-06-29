package native

import (
	"context"
	"strings"
	"testing"
	"time"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

func spec(name string, cmd ...string) runtime.WorkloadSpec {
	return runtime.WorkloadSpec{
		Name: name,
		Tier: tier.Native,
		Workload: v1.Workload{
			Metadata: v1.ObjectMeta{Name: name},
			Spec: v1.WorkloadSpec{
				Containers: []v1.Container{{Name: name, Command: cmd}},
			},
		},
	}
}

func TestNativeRunAndStatus(t *testing.T) {
	d := New()
	ctx := context.Background()
	// Long-lived process.
	if err := d.Ensure(ctx, spec("sleeper", "sleep", "30")); err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}
	st, _ := d.Status(ctx, "sleeper")
	if st.Phase != runtime.PhaseReady {
		t.Fatalf("phase = %s, want Ready", st.Phase)
	}
	if err := d.Stop(ctx, "sleeper"); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if err := d.Remove(ctx, "sleeper"); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
}

func TestNativeFailedProcess(t *testing.T) {
	d := New()
	ctx := context.Background()
	if err := d.Ensure(ctx, spec("boom", "false")); err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}
	// Give it a moment to exit.
	var st runtime.State
	for i := 0; i < 50; i++ {
		st, _ = d.Status(ctx, "boom")
		if st.Phase != runtime.PhaseReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.Phase != runtime.PhaseFailed {
		t.Fatalf("phase = %s, want Failed", st.Phase)
	}
}

func TestNativeLogsCapture(t *testing.T) {
	d := New()
	ctx := context.Background()
	if err := d.Ensure(ctx, spec("echoer", "echo", "hello-native")); err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}
	for i := 0; i < 50; i++ {
		if st, _ := d.Status(ctx, "echoer"); st.Phase != runtime.PhaseReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rc, err := d.Logs(ctx, "echoer", false)
	if err != nil {
		t.Fatalf("Logs() error: %v", err)
	}
	defer rc.Close()
	buf := make([]byte, 256)
	n, _ := rc.Read(buf)
	if !strings.Contains(string(buf[:n]), "hello-native") {
		t.Errorf("logs = %q, want to contain hello-native", string(buf[:n]))
	}
}

func TestNativeUnknown(t *testing.T) {
	d := New()
	st, _ := d.Status(context.Background(), "ghost")
	if st.Phase != runtime.PhaseUnknown {
		t.Errorf("phase = %s, want Unknown", st.Phase)
	}
}
