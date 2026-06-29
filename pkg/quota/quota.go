// Package quota translates Kubernetes-style ResourceRequirements (cpu, memory,
// and AI Box extended resources like aibox.io/npu and aibox.io/gpu) into the
// normalized limits the runtime drivers apply (FR-3). CPU is expressed in
// millicores, memory in bytes; extended resources are passed through as device
// requests for the containerd/VM drivers to honor.
package quota

import (
	"fmt"
	"strconv"
	"strings"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
)

// Extended resource keys recognized by the AI Box platform.
const (
	ResourceNPU = "aibox.io/npu"
	ResourceGPU = "aibox.io/gpu"
)

// Limits is the normalized resource budget for a workload.
type Limits struct {
	// CPUMillis is the CPU limit in millicores (e.g. "2" -> 2000, "500m" -> 500).
	CPUMillis int64
	// MemoryBytes is the memory limit in bytes (e.g. "2Gi" -> 2147483648).
	MemoryBytes int64
	// NPU is the number of NPU devices requested.
	NPU int64
	// GPU is the GPU request: a count, or the sentinel "shared".
	GPU string
	// Extended holds any other extended resources verbatim.
	Extended map[string]string
}

// FromContainer derives Limits from a container's resource limits. Requests are
// used as a fallback when a limit is unset.
func FromContainer(c v1.Container) (Limits, error) {
	get := func(key string) string {
		if v, ok := c.Resources.Limits[key]; ok {
			return v
		}
		return c.Resources.Requests[key]
	}

	var l Limits
	l.Extended = map[string]string{}

	if v := get("cpu"); v != "" {
		m, err := parseCPU(v)
		if err != nil {
			return Limits{}, fmt.Errorf("container %q cpu: %w", c.Name, err)
		}
		l.CPUMillis = m
	}
	if v := get("memory"); v != "" {
		b, err := parseMemory(v)
		if err != nil {
			return Limits{}, fmt.Errorf("container %q memory: %w", c.Name, err)
		}
		l.MemoryBytes = b
	}
	if v := get(ResourceNPU); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Limits{}, fmt.Errorf("container %q %s: %w", c.Name, ResourceNPU, err)
		}
		l.NPU = n
	}
	if v := get(ResourceGPU); v != "" {
		l.GPU = v
	}

	for k, v := range c.Resources.Limits {
		switch k {
		case "cpu", "memory", ResourceNPU, ResourceGPU:
		default:
			l.Extended[k] = v
		}
	}
	return l, nil
}

// parseCPU parses a k8s CPU quantity into millicores. Supports plain cores
// ("2", "0.5") and milli suffix ("500m").
func parseCPU(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "m") {
		n, err := strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(f * 1000), nil
}

// memSuffixes maps k8s memory suffixes to their byte multipliers.
var memSuffixes = []struct {
	suffix string
	mult   int64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
	{"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
	{"k", 1e3},
}

// parseMemory parses a k8s memory quantity into bytes.
func parseMemory(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, suf := range memSuffixes {
		if strings.HasSuffix(s, suf.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, suf.suffix), 64)
			if err != nil {
				return 0, err
			}
			return int64(n * float64(suf.mult)), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory quantity %q", s)
	}
	return n, nil
}
