# PoC: Evidence of Finding — path-level evidence in scan output

Proof of concept for [kubescape/kubescape#1563](https://github.com/kubescape/kubescape/issues/1563),
built for the LFX 2026 Term 3 project *"Evidence of Finding: Path-Level Evidence in Scan Output"*.

Branch: `feat/evidence-of-finding`, based on `upstream/master` at `aecc40c4`.
This is a PoC branch and is deliberately **not** raised as a pull request.

---

## The gap

A scan says a control failed on a resource, but not which field tripped it or what
that field holds. The data already exists — every rule emits failed, review, delete
and fix paths next to the object it fired on — but the only surface that showed
paths required **both** `--view resource` **and** `--verbose`.

## Try it

```bash
go build -o /tmp/kubescape .

cat > /tmp/demo.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: billing, namespace: prod}
spec:
  selector: {matchLabels: {app: billing}}
  template:
    metadata: {labels: {app: billing}}
    spec:
      containers:
        - name: api
          image: nginx:1.25
          env:
            - name: DB_PASSWORD
              value: s3cr3t-prod-pw
EOF

/tmp/kubescape scan framework nsa /tmp --show-evidence
```

```
Evidence:

  Deployment/prod/billing
    C-0012 Applications credentials in configuration files
      spec.template.spec.containers[0].env[0].name   DB_PASSWORD
      spec.template.spec.containers[0].env[0].value  <redacted>
    C-0017 Immutable container filesystem
      spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem  (not set)
                                                                               expected: true
    C-0030 Ingress and Egress blocked
      no field-level evidence for this control

  <redacted>: hidden because the value looks like a credential. Use --show-secrets to reveal.
```

`--format json --show-evidence` adds a machine-readable `evidence` array with
`redacted` and `masked` flags per item. Without the flag the JSON document is
unchanged.

## A bug this found in shipped code

`removeData` writes containers back as `[]corev1.Container`. The path walker added
in #2882 understood only `map[string]any` and `[]any`, so **every subscripted path
silently resolved to nothing** and container-level values never appeared on file
scans.

Same fixture, same command (`--view resource --verbose`):

| Binary | `(current: …)` rendered |
|---|---:|
| `upstream/master` @ `aecc40c4` | **0** |
| this branch | values render |

## Two measurements

**Rule corpus.** 186 of 257 regolibrary v2.0.1 rules (72.4%) emit at least one real
path; 71 cannot produce one at all — CIS host-file checks, cloud provider
descriptors, and rules like `naked-pods` that fail an object for existing. For those,
"no field-level evidence" is the correct output, not a gap. This independently
reproduces the 201/278 (72.3%) figure in the project description.

**Redaction false positives.** All 58 distinct field names that regolibrary rule
paths end in, run through the policy: exactly one matches
(`automountServiceAccountToken`), and it holds a bool, which is never treated as a
credential. Locked in as a regression test.

## What is implemented

| Deliverable | State |
|---|---|
| Path resolver over the captured object | done, handles typed Kubernetes values |
| `--show-evidence` / `-E` renderer | done |
| Redaction by default + `--show-secrets` | done |
| Tests across the three rule buckets | done |
| Evidence in JSON output (stretch) | done |
| Source file and line | **not done** — for the mentorship |
| Evidence contract docs for rule authors | **not done** — for the mentorship |

Source file and line is left out deliberately even though `locationresolver`
already exists, so the term has visible headline work.

## Design note

Evidence hangs off the report the JSON printer owns, beside severity, labels and
scan coverage, rather than off the session object. That is the existing pattern
here, and `jsonprinter.go` documents the reason: the session object is submitted to
the backend after local output is written, so enriching it would make `--format`
change the uploaded payload.

## Verification

`gofmt`, `go vet`, `go test -race ./core/... ./cmd/...` all clean.
Evidence package 87.9% statement coverage.
Exercised on a kind cluster, a manifest directory, a multi-document file, a Helm
chart and a Kustomize directory, across the pretty, JSON, SARIF, HTML and CSV
formats.

Collecting evidence for one workload across twenty failed controls costs 34 µs and
66 KB, walking the object rather than copying it.

## Known limits

- Container env values, Secret data and ConfigMap data are replaced by the scan
  before any printer runs, so `--show-secrets` cannot recover them. Those render as
  `<hidden by scan>` rather than as a value.
- Redaction matches field names by substring. Structural parents such as
  `secretKeyRef` and `tolerations` are excluded, but the exclusion list was built
  from observed cases, not a proof.
- The root cause of the typed-value bug is upstream: `removePodData` writing typed
  structs into a `map[string]any`. This branch makes the consumer tolerant instead,
  since that function sits behind a `DO NOT CHANGE` marker.
