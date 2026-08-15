# PoC: Native multi-cluster fleet posture aggregation

Proof of concept for the LFX 2026 Term 3 project *"Native Multi-Cluster Fleet Posture
Aggregation"*, which references
[kubescape/kubescape#2004](https://github.com/kubescape/kubescape/issues/2004).

Branch: `feat/fleet-posture-aggregation`, based on `upstream/master` at `355e4432`.
This is a PoC branch and is deliberately **not** raised as a pull request.

---

## The gap

Kubescape scans exactly one cluster per invocation, but almost every real user runs
several. There is no way to ask "which clusters fail C-0016?" or "staging passes this
and production fails it, so where did we drift?". People fall back to shell loops over
`KUBECONFIG` and hand-rolled `jq` merges.

## Try it

```bash
go build -o /tmp/kubescape .

/tmp/kubescape scan fleet --contexts kind-fleet-a,kind-fleet-b --baseline kind-fleet-a
```

```
+---------------+--------------+---------+------------+--------+--------+---------+
| CONTEXT       | CLUSTER      | SCANNED | COMPLIANCE | FAILED | PASSED | SKIPPED |
+---------------+--------------+---------+------------+--------+--------+---------+
| kind-fleet-a  | kind-fleet-a | yes     | 61.5%      |     23 |     29 |       9 |
| kind-fleet-b  | kind-fleet-b | yes     | 58.2%      |     25 |     27 |       9 |
+---------------+--------------+---------+------------+--------+--------+---------+

+---------+------------------------------------------+--------------+--------------+-------+
| CONTROL | NAME                                     | KIND-FLEET-A | KIND-FLEET-B | DRIFT |
+---------+------------------------------------------+--------------+--------------+-------+
| C-0016  | Allow privilege escalation               | pass         | fail         | yes   |
| C-0017  | Immutable container filesystem           | fail         | fail         |       |
+---------+------------------------------------------+--------------+--------------+-------+

Baseline: kind-fleet-a. 1 of 2 controls differ from it.
```

`--format json` emits the same report for CI. `--drift-only` shows just the controls
that differ from the baseline.

## Why the orchestrator is sequential, proved rather than asserted

The design rests on one claim: you cannot safely hold two live cluster clients at once.
Rather than assert that, the branch proves it with a test that is expected to fail:

```bash
go test -race -tags fleetrace ./core/pkg/fleet/ -run TestGlobalClientConfigRaces
```

```
WARNING: DATA RACE
Write at 0x0001054cb170 by goroutine 35:
  .../k8sinterface/k8sconfig.go:230
Write at 0x0001054cb178 by goroutine 35:
  .../k8sinterface/k8sconfig.go:231
Write at 0x0001053d8483 by goroutine 35:
  .../k8sinterface/k8sconfig.go:232
```

`SetClusterContextName` writes `clusterContextName`, and when the context changes it
also clears `K8SConfig`, `clientConfigAPI` and `connectedToCluster`. None of it is
locked. Two goroutines pointing the process at different clusters race on all four, and
the likely outcome is not a crash but a report attributed to the wrong cluster.

The test sits behind the `fleetrace` build tag so it never runs in normal CI, where a
deliberate race would be a failure rather than a result. When the client becomes
per-scan, this test stops reporting a race and concurrent scanning becomes safe to
build.

## A correction to the project description

The description says the Kubernetes client is a process-global singleton and cites issue
#2004. Reading the code, both halves need adjusting.

**#2004 is closed and already fixed.** It was about the `PolicyHandler` singleton
leaking one cluster's cached exceptions into the next scan. On master, `PolicyHandler`
is created per cluster through a registry keyed by cluster name
(`core/pkg/policyhandler/handlepullpolicies.go:47`, `registry.go`). There is even a
variant built for this exact access pattern:

> `NewPolicyHandlerWithRelease` returns the shared PolicyHandler for clusterName and a
> release function that must be deferred. Intended for orchestration paths ... that scan
> the same cluster sequentially in one goroutine

**The real blocker is in k8s-interface**, and it is worth noting that `go.mod` currently
replaces that module with a fork:

```
replace github.com/kubescape/k8s-interface => github.com/doraem-on/k8s-interface v0.0.218-...
```

The fork's `SetClusterContextName` has already been improved to reset cached config when
the context changes. It still races, which the test above demonstrates against the
version the build actually uses.

## What is implemented

| Deliverable from the description | State |
|---|---|
| `scan fleet` subcommand with `--contexts` and `--baseline` | done |
| Sequential orchestrator running a complete unmodified scan per context | done |
| `FleetReport` with a cross-cluster control matrix | done |
| Drift detection against a baseline cluster | done |
| At least one printer, usable from CLI and CI | done, table and JSON |
| Tests for orchestration, missing contexts, drift | done |
| Concurrency | out of scope on purpose, gated on the client refactor |

Deliberately left for the term: where `FleetReport` should finally live in the module
layout, and where OSS aggregation stops and platform aggregation begins. Both are named
in the description as open questions for the mentee, so the PoC does not pre-empt them.

## Design notes

**Nothing existing changed.** No report schema, printer or public API was touched. The
only change outside the new package is one three-line getter,
`ScanInfo.KubeconfigPath()`, plus registering the subcommand.

**Aggregation is separate from orchestration.** Everything in `report.go` and `drift.go`
is a pure function over `PostureReport` values, so the whole matrix and drift logic is
tested with no cluster and no kubeconfig.

**Skipped is its own state.** A control the baseline failed and another cluster skipped
counts as drift. Skipped means the control did not apply there, which is a real
difference and usually the interesting one. Collapsing it into pass would hide exactly
what a fleet report exists to show.

**A failed cluster is never silently dropped.** It stays in the report marked unscanned,
its controls are filled with `-` rather than left blank, and it is excluded from drift,
because a scan that failed says nothing about posture. One unreachable cluster must not
flag every control in the fleet.

## Verification

`gofmt`, `go vet ./...` and `go test ./... -short` are clean.
`go test -race` passes for the new package, `cmd/scan` and `core/cautils`.
Statement coverage on `core/pkg/fleet` is **98.9%**.

## Known limits

- Sequential only. A large fleet takes the sum of its clusters.
- The per-cluster scan uses one framework set for the whole fleet, chosen by
  `--frameworks`.
- `FleetReport` lives in `core/pkg/fleet` as a starting point, not a final answer.
