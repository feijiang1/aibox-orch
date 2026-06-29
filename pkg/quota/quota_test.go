package quota

import (
	"testing"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
)

func TestFromContainer(t *testing.T) {
	c := v1.Container{
		Name: "openclaw",
		Resources: v1.ResourceRequirements{
			Limits: map[string]string{
				"cpu":          "2",
				"memory":       "2Gi",
				ResourceNPU:    "1",
				ResourceGPU:    "shared",
				"aibox.io/foo": "bar",
			},
		},
	}
	l, err := FromContainer(c)
	if err != nil {
		t.Fatalf("FromContainer() error: %v", err)
	}
	if l.CPUMillis != 2000 {
		t.Errorf("CPUMillis = %d, want 2000", l.CPUMillis)
	}
	if l.MemoryBytes != 2*(1<<30) {
		t.Errorf("MemoryBytes = %d, want %d", l.MemoryBytes, 2*(1<<30))
	}
	if l.NPU != 1 {
		t.Errorf("NPU = %d, want 1", l.NPU)
	}
	if l.GPU != "shared" {
		t.Errorf("GPU = %q, want shared", l.GPU)
	}
	if l.Extended["aibox.io/foo"] != "bar" {
		t.Errorf("Extended[aibox.io/foo] = %q, want bar", l.Extended["aibox.io/foo"])
	}
}

func TestParseCPU(t *testing.T) {
	tests := map[string]int64{"2": 2000, "0.5": 500, "500m": 500, "100m": 100, "1": 1000}
	for in, want := range tests {
		got, err := parseCPU(in)
		if err != nil {
			t.Fatalf("parseCPU(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("parseCPU(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseMemory(t *testing.T) {
	tests := map[string]int64{
		"2Gi":  2 * (1 << 30),
		"512Mi": 512 * (1 << 20),
		"1Ki":  1024,
		"1000": 1000,
		"1M":   1_000_000,
	}
	for in, want := range tests {
		got, err := parseMemory(in)
		if err != nil {
			t.Fatalf("parseMemory(%q) error: %v", in, err)
		}
		if got != want {
			t.Errorf("parseMemory(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := parseMemory("abc"); err == nil {
		t.Error("expected error for invalid memory")
	}
	if _, err := parseCPU("xx"); err == nil {
		t.Error("expected error for invalid cpu")
	}
}

func TestFallbackToRequests(t *testing.T) {
	c := v1.Container{
		Name: "x",
		Resources: v1.ResourceRequirements{
			Requests: map[string]string{"cpu": "250m"},
		},
	}
	l, err := FromContainer(c)
	if err != nil {
		t.Fatalf("FromContainer() error: %v", err)
	}
	if l.CPUMillis != 250 {
		t.Errorf("CPUMillis = %d, want 250 (from requests)", l.CPUMillis)
	}
}
