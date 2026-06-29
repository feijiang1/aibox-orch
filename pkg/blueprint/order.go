package blueprint

import (
	"sort"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
)

// StartOrder returns workload names in dependency order: a workload always
// appears after all workloads it depends on. Within the same dependency level
// names are sorted alphabetically for deterministic bring-up. The blueprint is
// assumed already validated (acyclic, deps exist).
func StartOrder(bp *v1.Blueprint) []string {
	deps := make(map[string][]string, len(bp.Spec.Workloads))
	for _, w := range bp.Spec.Workloads {
		deps[w.Metadata.Name] = w.Spec.DependsOn
	}

	visited := make(map[string]bool, len(deps))
	var order []string

	var visit func(string)
	visit = func(n string) {
		if visited[n] {
			return
		}
		visited[n] = true
		d := append([]string(nil), deps[n]...)
		sort.Strings(d)
		for _, dep := range d {
			visit(dep)
		}
		order = append(order, n)
	}

	roots := make([]string, 0, len(deps))
	for name := range deps {
		roots = append(roots, name)
	}
	sort.Strings(roots)
	for _, name := range roots {
		visit(name)
	}
	return order
}
