// Package tier resolves a workload's isolation tier from its spec, using the
// idiomatic Kubernetes RuntimeClass mechanism plus an aibox.io/tier annotation
// escape hatch for the native tier (which has no RuntimeClass analogue).
package tier

import (
	"fmt"

	v1 "github.com/intel/aibox-orch/pkg/apis/aibox/v1"
)

// Tier is the isolation level a workload runs at.
type Tier string

const (
	// Native runs the workload as a host process (no container/VM isolation).
	Native Tier = "native"
	// Container runs the workload as a containerd container with the runc handler.
	Container Tier = "container"
	// Kata runs the workload in a KATA lightweight VM (for untrusted code, FR-2).
	Kata Tier = "kata"
	// ACRNVM runs the workload in an ACRN Secure VM.
	ACRNVM Tier = "acrn-vm"
)

// RuntimeClass names recognized in WorkloadSpec.RuntimeClassName.
const (
	runtimeClassRunc = "runc"
	runtimeClassKata = "kata"
	runtimeClassACRN = "acrn"
)

// Resolve determines the tier for a workload. Precedence:
//  1. aibox.io/tier annotation (explicit override; only "native" is meaningful
//     since the others are better expressed as RuntimeClasses).
//  2. runtimeClassName: kata -> Kata, acrn -> ACRNVM, runc/"" -> Container.
//
// It returns an error for an unknown annotation value or RuntimeClass so that
// manifests fail fast rather than silently running at the wrong isolation level.
func Resolve(w v1.Workload) (Tier, error) {
	if ann, ok := w.Metadata.Annotations[v1.TierAnnotation]; ok {
		switch Tier(ann) {
		case Native:
			return Native, nil
		case Container:
			return Container, nil
		case Kata:
			return Kata, nil
		case ACRNVM:
			return ACRNVM, nil
		default:
			return "", fmt.Errorf("workload %q: unknown %s annotation value %q",
				w.Metadata.Name, v1.TierAnnotation, ann)
		}
	}

	switch w.Spec.RuntimeClassName {
	case "", runtimeClassRunc:
		return Container, nil
	case runtimeClassKata:
		return Kata, nil
	case runtimeClassACRN:
		return ACRNVM, nil
	default:
		return "", fmt.Errorf("workload %q: unknown runtimeClassName %q",
			w.Metadata.Name, w.Spec.RuntimeClassName)
	}
}
