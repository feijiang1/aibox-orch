# aibox-orch

A thin, **Kubernetes/k3s-compatible** orchestration controller for the **NG AI Box**.
It brings up the whole AI Box stack from a single declarative blueprint manifest,
schedules each workload onto the correct **isolation tier**, and runs a
k3s-style reconcile loop for health and restart — with a **`docker compose`-style
CLI**.

> Implements NG-AIBOX-HLD-PLATORCH-001. See [`../NG_AIBOX_Orchestration.md`](../NG_AIBOX_Orchestration.md)
> for the requirement and [`../PLAN.md`](../PLAN.md) for the design.

## Concepts

- **Blueprint** — a k8s-style manifest (`apiVersion: aibox.io/v1`, `kind: Blueprint`)
  listing the workloads of one deployment profile (`home`, `nas-smb`, ...).
- **Isolation tier** — selected the idiomatic Kubernetes way via `runtimeClassName`
  (plus an `aibox.io/tier` annotation for the native tier):

  | Selector                         | Tier        | Backing driver                       |
  | -------------------------------- | ----------- | ------------------------------------ |
  | *(default)* / `runc`             | `container` | **containerd** (real)                |
  | annotation `aibox.io/tier: native` | `native`  | host process supervisor (real)       |
  | `runtimeClassName: kata`         | `kata`      | KATA VM — *stub* (untrusted code, FR-2) |
  | `runtimeClassName: acrn`         | `acrn-vm`   | ACRN Secure VM — *stub*              |

  The container and native drivers are fully functional. KATA/ACRN are interface-complete
  **stubs** sitting at the correct containerd-shim / hypervisor seam, to be promoted on
  reference hardware.

## CLI (compose parity)

```
aibox up      -f <blueprint.yaml>          # bring up the full stack   (compose: up -d)
aibox down    -f <blueprint.yaml>          # stop & remove everything  (compose: down)
aibox ps      -f <blueprint.yaml>          # workload status table     (compose: ps)
aibox restart -f <blueprint.yaml> <name>   # restart one workload      (compose: restart)
aibox logs    -f <blueprint.yaml> <name>   # workload logs             (compose: logs)
aibox run     -f <blueprint.yaml>          # up + foreground self-heal loop
```

Flags: `--containerd <sock>` (default `/run/containerd/containerd.sock`),
`--simulate` (use the simulated container driver — no containerd needed),
`--otlp <endpoint>` (emit OTLP traces; also honors `OTEL_EXPORTER_OTLP_ENDPOINT`).

## Build & test

```sh
go build ./...                 # build all packages
go test  ./...                 # unit tests (no external deps)
go build -o bin/aibox ./cmd/aibox

# Smoke test on a plain box (no containerd): all four tiers reach Ready.
./bin/aibox up -f examples/blueprints/home.yaml --simulate
```

### Integration test (requires a running containerd)

Gated behind the `integration` build tag; skips cleanly when no socket is present.

```sh
# with a reachable containerd socket (root or properly-configured rootless):
go test -tags integration ./test/integration/ -v \
    -args -containerd=/run/containerd/containerd.sock
```

It asserts the NFRs: full-stack cold-start **< 60 s** and single-workload restart **< 5 s**
with the others staying Ready.

## Package layout

```
cmd/aibox            compose-style CLI
pkg/apis/aibox/v1    Blueprint API types (k8s-shaped)
pkg/blueprint        manifest load + validate + dependency ordering
pkg/tier             RuntimeClass/annotation -> tier resolution
pkg/runtime          Driver interface + simulated FakeDriver
  ./containerd         real containerd driver (the k3s substrate)
  ./native             real host-process driver
  ./kata, ./acrn       stub VM-tier drivers
pkg/quota            k8s ResourceRequirements -> CPU/mem/NPU/GPU limits
pkg/reconcile        desired-vs-actual control loop, probes, restart
pkg/telemetry        OpenTelemetry / OTLP tracing
pkg/app              wires drivers + reconciler (containerd w/ simulate fallback)
test/integration     containerd-backed e2e test (build tag: integration)
```

## Requirement traceability

| Req   | Where                                                            |
| ----- | --------------------------------------------------------------- |
| FR-1  | `pkg/blueprint` + `reconcile.Up` (single-manifest bring-up)     |
| FR-2  | `pkg/tier` (`runtimeClassName: kata`) + `pkg/runtime/kata`       |
| FR-3  | `pkg/quota` -> containerd OCI/cgroup limits                     |
| FR-4  | `cmd/aibox` (compose-style commands)                            |
| NFR   | `reconcile` ordering + restart; asserted in the integration test|
| DoD   | per-package unit tests; `pkg/telemetry` OTLP traces             |
```
