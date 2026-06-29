# aibox-orch — Implementation Plan

**Status:** MVP implemented — open for review to decide next implementation steps
**Source spec:** NG_AIBOX_Orchestration.md (NG-AIBOX-HLD-PLATORCH-001) — the requirement/HLD this plan implements
**Built so far:** see [README.md](README.md) for the delivered MVP and requirement traceability
**Date:** 2026-06-27

---

## 1. Objective

Build `aibox-orch`: a **thin, k8s/k3s-compatible orchestration controller** for the NG AI Box.
It brings up the full AI Box stack from a single declarative blueprint manifest, schedules
each workload onto the correct **isolation tier** (native / container / KATA / ACRN Secure VM),
and runs a k3s-style reconcile loop for health and restart.

Scope of the first deliverable: **walking-skeleton MVP**.
- **Container tier (containerd) is fully functional.**
- **native** tier is real (process supervision).
- **KATA** and **ACRN-VM** tiers are real *interfaces* with stub implementations, sitting at the
  correct containerd-shim / hypervisor seam so they can be promoted on reference hardware later.

---

## 2. Key design decisions (locked)

| Decision | Choice | Rationale |
|---|---|---|
| Language | **Go** | k3s/k8s/containerd/Kata are all Go; reuse their client libs + API machinery. |
| Compatibility | **k8s/k3s-compatible thin client** | Adopt k8s API conventions + reconcile semantics; do NOT reimplement apiserver/etcd. |
| Container runtime | **containerd (via CRI / containerd Go client)** | This is what k3s actually uses; Docker was dropped from k8s. Makes RuntimeClass→tier native. |
| Tier selector | **k8s `RuntimeClass` + annotation** | `runtimeClassName: kata` → KATA, `acrn` → ACRN VM, default `runc` → container, annotation `aibox.io/tier: native` → native. Idiomatic k8s/Kata mechanism. |
| CLI UX | **`docker compose`-style** (FR-4) | One declarative engine, two front doors: compose-style CLI + k8s-style manifests. |
| Testing | **Tests written alongside each package** | Table-driven Go `testing`; fake Driver for reconciler. Not a trailing phase. |
| Target env | **Plain Linux dev box** | containerd run from snap-bundled binary or installed standalone; ACRN/KATA stubbed. |

---

## 3. Why containerd (not Docker)

1. k3s/k8s talk to **containerd over CRI**; Docker (dockershim) was removed in k8s 1.24.
2. containerd has first-class **runtime handlers** — `runtimeClassName: kata` maps directly to
   `io.containerd.kata.v2`; default is `runc`. Our tier selector becomes the real mechanism, not a
   Docker-specific translation.
3. KATA (`containerd-shim-kata-v2`) and ACRN (Kata hypervisor backend) integrate at the containerd
   shim layer — exactly where our stub drivers sit.
4. CRI provides a clean Go client (`k8s.io/cri-api`), reinforcing the thin-client framing.

**Env note:** snap Docker bundles `containerd`, `containerd-shim-runc-v2`, `ctr`, `runc`, but no
standalone containerd is currently running and Go is not installed. Both must be provisioned at
build time (flagged below).

---

## 4. Architecture

```
aibox-orch/
├── go.mod                         # k8s.io/api, apimachinery, cri-api, containerd Go client, sigs.k8s.io/yaml
├── cmd/aibox/                     # compose-style CLI: up/down/ps/logs/restart (FR-4)
├── pkg/apis/aibox/v1/             # Blueprint extension types + reuse of corev1.PodSpec
├── pkg/blueprint/                 # load/validate k8s-style manifests → desired state (FR-1)
│   └── blueprint_test.go
├── pkg/tier/                      # RuntimeClass/annotation → Tier resolution
│   └── tier_test.go
├── pkg/runtime/                   # Driver interface (CRI-flavored) + tier drivers
│   ├── driver.go                  #   interface + fake driver for tests
│   ├── containerd/                #   REAL — containerd Go client / CRI
│   │   └── containerd_test.go
│   ├── native/                    #   REAL — os/exec process supervision
│   │   └── native_test.go
│   ├── kata/                      #   STUB — runtime-handler seam (FR-2)
│   └── acrn/                      #   STUB — Secure VM seam
├── pkg/reconcile/                 # k3s-style controller loop, probes, restart (NFR)
│   └── reconcile_test.go          #   uses fake driver
├── pkg/quota/                     # ResourceRequirements → cgroup/runtime limits (FR-3)
│   └── quota_test.go
├── pkg/telemetry/                 # OTLP traces/metrics (DoD)
└── examples/blueprints/           # home.yaml, nas-smb.yaml (k8s-style + RuntimeClass)
```

### Driver interface (the swappable seam)
```go
type Driver interface {
    Ensure(ctx context.Context, spec WorkloadSpec) error // idempotent create+start
    Stop(ctx context.Context, name string) error
    Status(ctx context.Context, name string) (State, error) // Ready/Pending/Failed/Stopped
    Logs(ctx context.Context, name string) (io.ReadCloser, error)
    Tier() Tier
}
```
containerd + native are real; kata/acrn satisfy the same contract (log/simulate) so `aibox up`
succeeds end-to-end on this box.

### Sample blueprint (k8s-compatible, tier via RuntimeClass)
```yaml
apiVersion: aibox.io/v1
kind: Blueprint
metadata: { name: home }
spec:
  workloads:
    - metadata: { name: openclaw }
      spec:                              # standard corev1.PodSpec subset
        containers:
          - name: openclaw
            image: openclaw:latest
            resources:
              limits: { cpu: "2", memory: 2Gi, "aibox.io/npu": "0" }
            readinessProbe: { httpGet: { path: /healthz, port: 8080 } }
    - metadata: { name: sandbox-exec }
      spec:
        runtimeClassName: kata           # untrusted → KATA (FR-2)
        containers: [ ... ]
    - metadata: { name: credential-vault }
      spec:
        runtimeClassName: acrn           # Secure VM tenant
        containers: [ ... ]
```

---

## 5. CLI parity with `docker compose` (FR-4)

| Compose command | `aibox` equivalent | Action |
|---|---|---|
| `docker compose up -d` | `aibox up --blueprint home` | Apply manifest, reconcile to Ready |
| `docker compose down` | `aibox down` | Stop & remove all workloads |
| `docker compose ps` | `aibox ps` / `aibox status` | Table: workload + tier + state |
| `docker compose logs -f x` | `aibox logs -f openclaw` | Stream workload logs |
| `docker compose restart x` | `aibox restart openclaw` | Restart one workload (< 5s) |
| `docker compose up` (re-apply) | `aibox up` | Rolling-update changed workloads |

---

## 6. Build order (each step ships code + tests, and is runnable)

1. **Toolchain**: install Go; provision/run containerd (snap-bundled or standalone); `go mod init`.
2. **Blueprint loader** + validation reusing apimachinery/corev1 types + sample manifests + `blueprint_test.go`.
3. **Tier resolution** from `runtimeClassName`/annotation + `tier_test.go`.
4. **Driver interface + fake driver**; **containerd driver (real)** + native driver + their `_test.go`.
5. **Reconcile loop**: dependency-ordered bring-up, probes, restart policy + `reconcile_test.go` (fake driver).
6. **Compose-style CLI** (`up`/`down`/`ps`/`logs`/`restart`).
7. **KATA + ACRN stub drivers** (interface-complete).
8. **OTLP telemetry**.
9. **Integration test**: `aibox up` against real containerd with public images; assert full stack
   Ready < 60s (NFR) and `restart` < 5s (NFR).

---

## 7. Requirement traceability

| Req | Covered by |
|---|---|
| FR-1 single-manifest bring-up | blueprint loader + reconcile |
| FR-2 untrusted → KATA | tier resolution (`runtimeClassName: kata`) + kata driver |
| FR-3 resource quotas | quota pkg → containerd/cgroup limits |
| FR-4 compose-style CLI | cmd/aibox |
| NFR cold-start < 60s | reconcile dependency ordering + integration test |
| NFR restart < 5s | reconcile restart policy + integration test |
| DoD tests + OTLP | per-package tests + telemetry pkg |

---

## 8. Open items / provisioning needed before/at step 1

1. **Install Go** (not present on box) — confirm OK.
2. **Run containerd** — use snap-bundled binary or install standalone; needs a reachable socket.
3. **k8s + containerd Go modules** via `go mod download` — needs network access; confirm no restriction.
4. Confirm **RuntimeClass-as-tier-selector** is the intended compatibility approach (idiomatic k8s/Kata).

---

## 9. Assumptions

- The four `[[...]]` related design docs are not in this workspace; workload set (MS1–MS6, OpenClaw,
  model serving, dashboard) is modeled as opaque named workloads — real images slot in later.
- KATA/ACRN are stubbed by design for the MVP; promotion to real drivers is a later phase on the
  Intel Core Ultra reference hardware.
