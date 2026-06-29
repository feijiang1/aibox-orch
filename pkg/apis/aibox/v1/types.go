// Package v1 defines the AI Box Blueprint API types.
//
// The types intentionally mirror Kubernetes API conventions
// (apiVersion/kind/metadata/spec, a PodSpec-like workload spec, container
// resources and probes, and the standard runtimeClassName selector) so that a
// Blueprint workload is structurally convertible to a Kubernetes Pod. This is
// what makes aibox-orch a "k8s/k3s-compatible thin client": we adopt the API
// shape without depending on the full apiserver machinery.
package v1

// GroupVersion is the apiVersion string Blueprints are expected to carry.
const GroupVersion = "aibox.io/v1"

// Kind is the manifest kind handled by the orchestrator.
const Kind = "Blueprint"

// Blueprint is the top-level declarative manifest: the full set of workloads
// that make up one AI Box deployment profile (e.g. "home", "nas-smb").
type Blueprint struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       BlueprintSpec `json:"spec"`
}

// BlueprintSpec holds the workload set for a blueprint.
type BlueprintSpec struct {
	Workloads []Workload `json:"workloads"`
}

// ObjectMeta is the trimmed-down equivalent of k8s metav1.ObjectMeta.
type ObjectMeta struct {
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Workload is one schedulable unit. Its Spec mirrors a Kubernetes PodSpec
// subset; the isolation tier is selected via RuntimeClassName (idiomatic
// k8s/Kata mechanism) or the aibox.io/tier annotation.
type Workload struct {
	Metadata ObjectMeta   `json:"metadata"`
	Spec     WorkloadSpec `json:"spec"`
}

// WorkloadSpec is a PodSpec-like description of how to run a workload.
type WorkloadSpec struct {
	// RuntimeClassName selects the runtime handler / isolation tier.
	// "" or "runc" -> container tier; "kata" -> KATA; "acrn" -> ACRN Secure VM.
	RuntimeClassName string `json:"runtimeClassName,omitempty"`

	// Containers is the list of containers to run (only the first is required
	// for the MVP, matching the common single-container Pod case).
	Containers []Container `json:"containers"`

	// DependsOn lists workload names that must reach Ready before this one
	// starts. Drives bring-up ordering in the reconciler.
	DependsOn []string `json:"dependsOn,omitempty"`

	// RestartPolicy mirrors k8s semantics: Always (default), OnFailure, Never.
	RestartPolicy RestartPolicy `json:"restartPolicy,omitempty"`
}

// Container mirrors the essential fields of a k8s core/v1 Container.
type Container struct {
	Name           string               `json:"name"`
	Image          string               `json:"image"`
	Command        []string             `json:"command,omitempty"`
	Args           []string             `json:"args,omitempty"`
	Env            []EnvVar             `json:"env,omitempty"`
	Resources      ResourceRequirements `json:"resources,omitempty"`
	ReadinessProbe *Probe               `json:"readinessProbe,omitempty"`
}

// EnvVar is a name/value environment entry.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ResourceRequirements mirrors k8s requests/limits. Values are quantity
// strings (e.g. "2", "500m", "2Gi") plus AI Box extended resources such as
// "aibox.io/npu" and "aibox.io/gpu".
type ResourceRequirements struct {
	Limits   map[string]string `json:"limits,omitempty"`
	Requests map[string]string `json:"requests,omitempty"`
}

// Probe describes a readiness check. Exactly one handler should be set.
type Probe struct {
	HTTPGet             *HTTPGetAction `json:"httpGet,omitempty"`
	Exec                *ExecAction    `json:"exec,omitempty"`
	TCPSocket           *TCPSocketAction `json:"tcpSocket,omitempty"`
	InitialDelaySeconds int            `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int            `json:"periodSeconds,omitempty"`
	TimeoutSeconds      int            `json:"timeoutSeconds,omitempty"`
	FailureThreshold    int            `json:"failureThreshold,omitempty"`
}

// HTTPGetAction is an HTTP readiness handler.
type HTTPGetAction struct {
	Path string `json:"path,omitempty"`
	Port int    `json:"port"`
}

// ExecAction is a command-based readiness handler.
type ExecAction struct {
	Command []string `json:"command"`
}

// TCPSocketAction is a TCP-connect readiness handler.
type TCPSocketAction struct {
	Port int `json:"port"`
}

// RestartPolicy enumerates restart behavior.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "Always"
	RestartOnFailure RestartPolicy = "OnFailure"
	RestartNever     RestartPolicy = "Never"
)

// TierAnnotation is the annotation key used to force a tier (notably "native",
// which has no RuntimeClass analogue).
const TierAnnotation = "aibox.io/tier"
