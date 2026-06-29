package blueprint

import (
	"strings"
	"testing"
)

const validHome = `
apiVersion: aibox.io/v1
kind: Blueprint
metadata:
  name: home
spec:
  workloads:
    - metadata: { name: ima }
      spec:
        containers:
          - name: ima
            image: ima:latest
    - metadata: { name: openclaw }
      spec:
        dependsOn: [ima]
        containers:
          - name: openclaw
            image: openclaw:latest
            readinessProbe:
              httpGet: { path: /healthz, port: 8080 }
    - metadata: { name: sandbox }
      spec:
        runtimeClassName: kata
        containers:
          - name: sandbox
            image: sandbox:latest
`

func TestParseValid(t *testing.T) {
	bp, err := Parse([]byte(validHome))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if bp.Metadata.Name != "home" {
		t.Errorf("name = %q, want home", bp.Metadata.Name)
	}
	if len(bp.Spec.Workloads) != 3 {
		t.Fatalf("got %d workloads, want 3", len(bp.Spec.Workloads))
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"bad apiVersion", strings.Replace(validHome, "aibox.io/v1", "v1", 1), "unsupported apiVersion"},
		{"bad kind", strings.Replace(validHome, "kind: Blueprint", "kind: Pod", 1), "unsupported kind"},
		{"no workloads", "apiVersion: aibox.io/v1\nkind: Blueprint\nmetadata: {name: x}\nspec: {workloads: []}\n", "no workloads"},
		{"unknown dep", strings.Replace(validHome, "dependsOn: [ima]", "dependsOn: [ghost]", 1), "unknown workload"},
		{"unknown field", validHome + "  bogusField: 1\n", "parse blueprint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestDuplicateName(t *testing.T) {
	dup := `
apiVersion: aibox.io/v1
kind: Blueprint
metadata: { name: home }
spec:
  workloads:
    - metadata: { name: a }
      spec: { containers: [{ name: a, image: x }] }
    - metadata: { name: a }
      spec: { containers: [{ name: a, image: y }] }
`
	if _, err := Parse([]byte(dup)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestCycleDetection(t *testing.T) {
	cyclic := `
apiVersion: aibox.io/v1
kind: Blueprint
metadata: { name: home }
spec:
  workloads:
    - metadata: { name: a }
      spec: { dependsOn: [b], containers: [{ name: a, image: x }] }
    - metadata: { name: b }
      spec: { dependsOn: [a], containers: [{ name: b, image: y }] }
`
	if _, err := Parse([]byte(cyclic)); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestStartOrder(t *testing.T) {
	bp, err := Parse([]byte(validHome))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	order := StartOrder(bp)
	pos := make(map[string]int, len(order))
	for i, n := range order {
		pos[n] = i
	}
	if pos["ima"] > pos["openclaw"] {
		t.Errorf("ima (%d) should come before openclaw (%d)", pos["ima"], pos["openclaw"])
	}
	if len(order) != 3 {
		t.Errorf("order has %d entries, want 3", len(order))
	}
}
