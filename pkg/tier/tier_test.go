package tier

import (
	"testing"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
)

func workload(name, runtimeClass string, ann map[string]string) v1.Workload {
	return v1.Workload{
		Metadata: v1.ObjectMeta{Name: name, Annotations: ann},
		Spec:     v1.WorkloadSpec{RuntimeClassName: runtimeClass},
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		w       v1.Workload
		want    Tier
		wantErr bool
	}{
		{"default empty runtimeclass -> container", workload("a", "", nil), Container, false},
		{"explicit runc -> container", workload("a", "runc", nil), Container, false},
		{"kata runtimeclass -> kata", workload("sandbox", "kata", nil), Kata, false},
		{"acrn runtimeclass -> acrn-vm", workload("vault", "acrn", nil), ACRNVM, false},
		{"native annotation override", workload("a", "", map[string]string{v1.TierAnnotation: "native"}), Native, false},
		{"annotation wins over runtimeclass", workload("a", "kata", map[string]string{v1.TierAnnotation: "native"}), Native, false},
		{"unknown runtimeclass errors", workload("a", "gvisor", nil), "", true},
		{"unknown annotation errors", workload("a", "", map[string]string{v1.TierAnnotation: "bogus"}), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.w)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %q, want %q", got, tt.want)
			}
		})
	}
}
