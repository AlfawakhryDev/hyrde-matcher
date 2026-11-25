# Hyrde matching service

Hyrde places forward deployed engineers on milestones for US real estate and
construction software companies. This repository is the Go backend that
decides, in well under a millisecond, which engineers to offer a milestone,
and keeps every engineer's track record current from a stream of milestone
events.

```mermaid
flowchart LR
    web[Hyrde web app] -- POST /v1/match --> matcher
    web -- POST /v1/events --> matcher
    matcher -- milestone events --> kafka[(Kafka)]
    matcher -. match.served audit, async .-> kafka
    kafka --> statsd
    statsd -- exactly-once counters --> ddb[(DynamoDB)]
    ddb -- snapshot every few seconds --> matcher
```

## What happens on a request

1. **`POST /v1/match`** names the integrations a milestone touches (Procore,
   QuickBooks, Yardi…), the payout and the estimated hours.
2. The matcher answers from an **immutable in-memory snapshot**: an inverted
   index from skill to engineers, so only engineers who share a skill are
   scored. No database or network call is on this path.
3. Each candidate gets a score with its reasons: skill coverage (50%),
   reliability (25%, on-time delivery and first-time approval, Laplace
   smoothed so newcomers start neutral), free capacity (15%) and whether
   their rate fits the payout (10%). Only the top K are kept, in a small
   sorted slice, and only those are fully built.
4. A `match.served` audit event is queued to Kafka **without blocking**: if
   the buffer is ever full the event is dropped and counted, never the
   response delayed.

## What happens on an event

1. **`POST /v1/events`** (`milestone.assigned`, `.delivered`, `.reviewed`)
   waits for Kafka's acknowledgement before answering `202`, so an accepted
   event is never lost. Events are keyed by engineer, so one engineer's
   events stay ordered on one partition.
2. **`statsd`** consumes them in a consumer group (replicas share
   partitions) and applies each one in a **single DynamoDB transaction**:
   record the event id *if it is new* and add to the counters. A redelivered
   event fails the first condition and changes nothing, which turns Kafka's
   at-least-once delivery into exactly-once counting.
3. Offsets are committed only after DynamoDB has the change. Transient
   failures retry with exponential backoff; events that can never apply
   (bad JSON, unknown engineer) are skipped and counted.
4. The matcher's refresher picks the new counters up on its next snapshot.
   If a refresh fails it keeps serving the last good snapshot, and
   `/readyz` turns red only when that snapshot is too old to trust.

## Measured

| | |
|---|---|
| Ranking 10,000 engineers (`make bench`, Apple M-series) | **~64 µs**, 10 allocations per request |
| Throughput, 32 concurrent clients (`make load`, Docker on a laptop) | **~29,600 req/s**, 0 failures |
| Latency at the client, through Docker networking | p50 1.0 ms · p99 2.3 ms |
| Latency inside the matcher (Prometheus histogram) | 97.8% under 0.5 ms · 99.7% under 1 ms |
| Same checks on a shared 4-vCPU GitHub Actions runner (CI, every push) | 157 µs per ranking · 11,600 req/s · p99 9.2 ms |

The first version scored and sorted every candidate: 477 µs and 3,042
allocations per request. Top-K selection, a bitset instead of a map for
de-duplication, and building results only for the winners took it to 64 µs
and 10 allocations.

## Run it

```bash
make up      # Kafka (Redpanda), DynamoDB Local, matcher, statsd
make seed    # tables + 2,000 demo engineers
make e2e     # end-to-end test against the real stack
make load    # latency percentiles
make kind-up # the same services on a local Kubernetes cluster
```

```bash
curl -H "Authorization: Bearer dev-token" localhost:8080/v1/match \
  -d '{"milestoneId":"m1","integrations":["Procore","QuickBooks"],"payoutCents":200000,"estimatedHours":40}'
```

## Kubernetes

`deploy/k8s/base` is the production shape (EKS, MSK, DynamoDB):

- Three matcher replicas spread across zones, an HPA up to 20, and a
  PodDisruptionBudget that keeps two serving through node upgrades.
- Readiness means "index loaded and fresh", so a pod with stale data leaves
  the load balancer on its own. A preStop pause and graceful shutdown let
  rolling deploys finish in-flight requests.
- A memory limit but no CPU limit (CPU throttling hurts tail latency).
- Non-root numeric user, read-only root filesystem, all capabilities
  dropped, a distroless ~7 MB image.
- DynamoDB access through an IAM role on the service account (IRSA), so no
  AWS keys live in the cluster.

`deploy/k8s/local` overlays it for kind against the docker-compose stack.

## Layout

```
cmd/matcher     HTTP service: match + event intake
cmd/statsd      Kafka consumer → DynamoDB
cmd/seed        demo data
cmd/loadgen     load generator with percentiles
internal/match  index, ranking, refresher (the hot path)
internal/events event types, what each does to the counters, Kafka producer
internal/store  DynamoDB: scans, idempotent transactional updates
internal/api    handlers, auth middleware, metrics
internal/fde    Hyrde's pricing rules (milestone quotes, payout + margin)
e2e             end-to-end test against the running stack
```

## Tests

`make test` runs the unit tests (ranking, event effects, handlers through
`httptest`, refresher fallback, pricing). `make race` adds the race detector.
`make e2e` runs against real Kafka and DynamoDB and checks that redelivered
events count once. CI runs all three plus the benchmark and a load test on
every push.
