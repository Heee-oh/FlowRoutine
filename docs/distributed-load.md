# Distributed load security

FlowRoutine distributed execution is opt-in. The desktop application continues to start one local engine unless a
caller explicitly constructs the distributed coordinator. Native workers accept only HTTPS control traffic with a
verified client certificate and require TLS 1.3 or newer.

## Trust model

- Use a dedicated private CA for coordinator identities and a dedicated CA for worker identities. Do not reuse a
  public web certificate or a general corporate client certificate.
- Give every worker certificate a SAN matching the hostname or IP used by the coordinator. Give each worker a
  stable `-worker-id`; the protocol rejects a response whose identity differs from the configured target.
- Restrict worker ports with a host firewall or private network policy even though mTLS is mandatory. The default
  listen address is loopback (`127.0.0.1:9443`); exposing it is an explicit operator choice.
- Store private keys with OS-level access controls and rotate the CA or leaf certificates through the normal PKI
  process. Restart workers after rotation; certificates are loaded at process start.
- The coordinator rejects `http://`, missing client certificates, missing trust roots, and
  `InsecureSkipVerify`. The worker also rejects requests that do not have a verified peer chain.

Start a worker:

```bash
go run ./cmd/flowroutine-worker \
  -worker-id worker-1 \
  -listen 10.0.0.12:9443 \
  -tls-cert worker.pem \
  -tls-key worker-key.pem \
  -client-ca coordinator-ca.pem
```

The coordinator TLS configuration needs its client certificate, private key, worker CA, and the expected worker
server name. `LoadClientTLSConfig` and `LoadServerTLSConfig` apply the required TLS policy.

## Plans and runtime bindings

Every plan uses schema version 1 and includes a stable ID plus a caller-managed revision. Workers validate
and compile a plan before accepting a scheduled start, then return a SHA-256 digest of the plan they prepared.
The coordinator checks that digest before starting any worker.

Keep credentials out of the plan. Use placeholders such as `{{SECRET_TOKEN}}` and pass the value through
`RuntimeBindings`. Bindings are sent only in the authenticated, encrypted prepare request, copied into the engine's
in-memory runtime scope, and never included in status, snapshot, or error responses. Missing bindings fail plan
validation. A runtime binding cannot be overwritten by a response capture.

## Scheduling and load partitioning

Before preparation, the coordinator probes all selected workers. It estimates clock offset from the midpoint of
the request round trip, chooses a shared coordinator start time with a safety lead, and translates that time to
each worker clock. Workers reject starts that are already due or unreasonably far in the future.

The coordinator creates only as many active shards as the plan has positive capacity. Integer remainder assignment
is deterministic and preserves the configured totals for:

- legacy virtual users and global RPS caps;
- constant or ramping VU targets;
- constant or ramping arrival-rate targets;
- pre-allocated and maximum arrival workers; and
- maximum connections per host.

All workers receive the same scenario and plan revision, but each receives its own validated load shard.

## Metrics and partial failure

Workers return cumulative counters, HTTP status counts, branch-route selections/request totals, and the engine's
fixed 1,024-bucket latency histograms for the run and each request step. The coordinator sums buckets and
recomputes P95/P99/P99.9; it never averages worker percentiles. Branch-route descriptors and counters are also
validated for shape and monotonicity before aggregation. The aggregate can be converted with
`bridge.BuildMetricsBatchWithBranches`, so local and distributed reports use the same frontend schema.

Every new worker snapshot must be internally consistent and monotonic. When a worker is unreachable or returns
invalid/regressing metrics, the coordinator:

1. keeps that worker's last valid cumulative snapshot;
2. marks the worker `reachable: false` or `stale: true` with the latest error; and
3. marks the aggregate `partial: true` without decreasing previously accepted totals.

An initial probe, prepare, or schedule failure aborts the distributed start and stops workers that were already
prepared. A failure after start remains visible in `AggregateResult.Workers`; callers decide whether a partial run
can satisfy their SLO policy.
