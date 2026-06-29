package kata

import (
	"context"
	"testing"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
	"github.com/intel/aibox-orch/pkg/runtime"
	"github.com/intel/aibox-orch/pkg/tier"
)

func TestKataLifecycle(t *testing.T) {
	d := New(nil)
	if d.Tier() != tier.Kata {
		t.Fatalf("Tier() = %s, want kata", d.Tier())
	}
	ctx := context.Background()
	spec := runtime.WorkloadSpec{
		Name: "sandbox",
		Tier: tier.Kata,
		Workload: v1.Workload{
			Metadata: v1.ObjectMeta{Name: "sandbox"},
			Spec:     v1.WorkloadSpec{Containers: []v1.Container{{Name: "s", Image: "sandbox:latest"}}},
		},
	}
	if err := d.Ensure(ctx, spec); err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}
	// Stub becomes Ready promptly.
	st, _ := d.Status(ctx, "sandbox")
	if st.Phase != runtime.PhaseReady {
		t.Fatalf("phase = %s, want Ready", st.Phase)
	}
	if err := d.Remove(ctx, "sandbox"); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
}
