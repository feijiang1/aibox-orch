// Package blueprint loads and validates AI Box blueprint manifests (k8s-style
// YAML) into the typed v1.Blueprint desired state consumed by the reconciler.
package blueprint

import (
	"fmt"
	"os"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
	"github.com/intel/aibox-orch/pkg/tier"
	"sigs.k8s.io/yaml"
)

// Load reads, parses and validates a blueprint manifest from a file path.
func Load(path string) (*v1.Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read blueprint %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes a blueprint from YAML (or JSON, since YAML is a superset) bytes
// and validates it. sigs.k8s.io/yaml converts YAML->JSON first, so the json
// struct tags on the v1 types drive decoding exactly as in Kubernetes.
func Parse(data []byte) (*v1.Blueprint, error) {
	var bp v1.Blueprint
	if err := yaml.UnmarshalStrict(data, &bp); err != nil {
		return nil, fmt.Errorf("parse blueprint: %w", err)
	}
	if err := Validate(&bp); err != nil {
		return nil, err
	}
	return &bp, nil
}

// Validate checks structural and semantic invariants the reconciler relies on:
// correct apiVersion/kind, unique workload names, a container per workload,
// resolvable tiers, and a dependency graph that references real workloads and
// is acyclic.
func Validate(bp *v1.Blueprint) error {
	if bp.APIVersion != v1.GroupVersion {
		return fmt.Errorf("unsupported apiVersion %q (want %q)", bp.APIVersion, v1.GroupVersion)
	}
	if bp.Kind != v1.Kind {
		return fmt.Errorf("unsupported kind %q (want %q)", bp.Kind, v1.Kind)
	}
	if bp.Metadata.Name == "" {
		return fmt.Errorf("blueprint metadata.name is required")
	}
	if len(bp.Spec.Workloads) == 0 {
		return fmt.Errorf("blueprint %q has no workloads", bp.Metadata.Name)
	}

	names := make(map[string]bool, len(bp.Spec.Workloads))
	for i := range bp.Spec.Workloads {
		w := &bp.Spec.Workloads[i]
		if w.Metadata.Name == "" {
			return fmt.Errorf("workload[%d] has empty metadata.name", i)
		}
		if names[w.Metadata.Name] {
			return fmt.Errorf("duplicate workload name %q", w.Metadata.Name)
		}
		names[w.Metadata.Name] = true

		if len(w.Spec.Containers) == 0 {
			return fmt.Errorf("workload %q has no containers", w.Metadata.Name)
		}
		for j := range w.Spec.Containers {
			c := &w.Spec.Containers[j]
			if c.Name == "" {
				return fmt.Errorf("workload %q container[%d] has empty name", w.Metadata.Name, j)
			}
			if c.Image == "" {
				return fmt.Errorf("workload %q container %q has empty image", w.Metadata.Name, c.Name)
			}
		}

		// Tier must resolve (catches bad runtimeClassName / annotation early).
		if _, err := tier.Resolve(*w); err != nil {
			return err
		}

		if w.Spec.RestartPolicy != "" {
			switch w.Spec.RestartPolicy {
			case v1.RestartAlways, v1.RestartOnFailure, v1.RestartNever:
			default:
				return fmt.Errorf("workload %q has invalid restartPolicy %q", w.Metadata.Name, w.Spec.RestartPolicy)
			}
		}
	}

	// Dependencies must reference existing workloads.
	for _, w := range bp.Spec.Workloads {
		for _, dep := range w.Spec.DependsOn {
			if !names[dep] {
				return fmt.Errorf("workload %q depends on unknown workload %q", w.Metadata.Name, dep)
			}
			if dep == w.Metadata.Name {
				return fmt.Errorf("workload %q depends on itself", w.Metadata.Name)
			}
		}
	}

	if cycle := findCycle(bp.Spec.Workloads); cycle != "" {
		return fmt.Errorf("dependency cycle detected: %s", cycle)
	}
	return nil
}

// findCycle returns a human-readable cycle path if the dependsOn graph has one,
// or "" if it is acyclic. Standard DFS with a recursion stack.
func findCycle(workloads []v1.Workload) string {
	deps := make(map[string][]string, len(workloads))
	for _, w := range workloads {
		deps[w.Metadata.Name] = w.Spec.DependsOn
	}

	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS stack
		black = 2 // fully explored
	)
	color := make(map[string]int, len(workloads))
	var path []string

	var visit func(string) string
	visit = func(n string) string {
		color[n] = gray
		path = append(path, n)
		for _, d := range deps[n] {
			switch color[d] {
			case gray:
				return fmt.Sprintf("%v -> %s", path, d)
			case white:
				if c := visit(d); c != "" {
					return c
				}
			}
		}
		path = path[:len(path)-1]
		color[n] = black
		return ""
	}

	for _, w := range workloads {
		if color[w.Metadata.Name] == white {
			if c := visit(w.Metadata.Name); c != "" {
				return c
			}
		}
	}
	return ""
}
