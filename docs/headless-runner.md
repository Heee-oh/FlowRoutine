# Headless scenario runner

`flowroutine-run` executes a versioned FlowRoutine scenario without the desktop application. It uses the same
bridge preflight, configuration normalization, scenario compiler, load engine, metric batching, and SLO semantics
as desktop runs.

## Run in CI

Build the runner and pass one scenario file:

```bash
go build -o flowroutine-run ./cmd/flowroutine-run
./flowroutine-run \
  -json-report report.json \
  -junit-report junit.xml \
  examples/headless/ci-smoke.flowroutine.json
```

The JSON report defaults to stdout when `-json-report` is omitted. JUnit output is optional. The committed smoke
scenario targets `127.0.0.1:18080`; the repository CI workflow starts a loopback server before running it.

Exit codes are stable for CI automation:

| Code | Meaning |
| ---: | --- |
| `0` | Execution completed and no SLO check failed |
| `2` | Scenario, binding, or preflight validation failed |
| `3` | Execution, cancellation, or requested report output failed |
| `4` | Execution completed but at least one SLO check failed |

An enabled P95 or P99 gate with fewer than 20 or 100 samples is `insufficient`, respectively. It exits `0` and is
reported as a skipped JUnit check, matching the desktop tri-state result. Set a threshold to `0` to disable it.
Omitted quality-gate fields use the desktop defaults: 1% failure rate, 500 ms P95, 1000 ms P99, and no minimum RPS.

## Scenario format

The top-level `schemaVersion` is currently `1`. `scenario.config` is the execution-ready `LoadConfig` produced by
the desktop graph compiler; unknown JSON fields, trailing JSON values, files above 5 MiB, and unsupported versions
are rejected. Each scenario also carries a stable ID, display name, positive revision, metric batch interval,
quality gate, and an explicit list of required runtime bindings.

Use [the committed CI scenario](../examples/headless/ci-smoke.flowroutine.json) as the canonical minimal example.
Visual graph files are not accepted directly because headless and desktop execution both consume the compiled
configuration after graph validation.

## Runtime secrets

Store placeholders such as `{{SECRET_TOKEN}}` in the scenario and declare every name in
`requiredRuntimeBindings`. Supply values only through an environment-variable reference or regular file:

```bash
FLOWROUTINE_API_TOKEN='runtime-only-value' \
  ./flowroutine-run \
  -bind-env SECRET_TOKEN=FLOWROUTINE_API_TOKEN \
  scenario.flowroutine.json

./flowroutine-run \
  -bind-file SECRET_TOKEN=/run/secrets/api-token \
  scenario.flowroutine.json
```

The CLI never accepts an inline secret-value flag. A single trailing CR/LF is removed from binding files; values
are capped at 64 KiB and may not contain NUL, CR, or LF. Missing, empty, duplicate, undeclared, or unused bindings
fail validation by name without printing their values.

Scenario validation rejects literal credentials in URL user-info, sensitive query parameters, authentication
headers, JSON sensitive-key fields, and form-like bodies. Arbitrary opaque request bodies cannot be classified
reliably, so keep all credentials in declared `SECRET_*` placeholders. Runtime values are not included in JSON,
JUnit, or preflight output.
