# Nanga — Project Structure

Companion to [`spec.md`](./spec.md). Designed so **one engineer** can build, run, test and deploy the whole system.

## 1. Approach

- **Monorepo, single Go module.** One `go.mod`, one CI pipeline, atomic cross-service changes, no version juggling.
- **Six deployables, not nine.** The spec's nine logical services are grouped by data ownership and change frequency. Each logical service stays a separate package with its own hexagonal boundary, so it can be split into its own binary later without a rewrite.
- **One Dockerfile, one Makefile, one compose file.** Everything is parameterised by service name.
- **Hexagonal, kept light.** Each module has three folders (`domain`, `app`, `adapter`); ports are plain interfaces in `app`.

### Spec services → deployables

| Deployable | Logical services included | Why grouped |
|---|---|---|
| `gateway` | Public REST edge | Only HTTP-facing process (besides callbacks via the adapter). |
| `identity` | Identity + Customer/KYC | Same lifecycle and personal-data boundary. |
| `ledger` | Ledger + Wallet | Balances and entries must commit in one DB transaction. |
| `payments` | Payments orchestrator | Sagas, state machine, idempotency. |
| `mpesa` | M-Pesa adapter | Sole Daraja contact point; isolated for security and circuit breaking. |
| `worker` | Notification + Reconciliation | Background jobs and event consumers; no inbound API. |

## 2. Directory Tree

```
nanga/
├── cmd/                          # one tiny main.go per deployable (wiring only)
│   ├── gateway/main.go
│   ├── identity/main.go
│   ├── ledger/main.go
│   ├── payments/main.go
│   ├── mpesa/main.go
│   └── worker/main.go
│
├── internal/
│   ├── gateway/                  # HTTP adapter only: Gin routes, middleware, gRPC clients
│   │   ├── http/                 #   handlers, router, middleware (auth, rate limit, recovery)
│   │   └── client/               #   gRPC client wrappers to internal services
│   │
│   ├── identity/                 # identity + customer/KYC
│   │   ├── domain/               #   User, Credential, Token, KYCTier, limits tier
│   │   ├── app/                  #   use cases + ports.go (repository, hasher, token maker)
│   │   └── adapter/              #   grpc/, postgres/
│   │
│   ├── ledger/                   # ledger + wallet
│   │   ├── domain/               #   Journal, Entry, Wallet, Hold, balance invariants
│   │   ├── app/                  #   Post, Hold/Release, GetBalance + ports.go
│   │   └── adapter/              #   grpc/, postgres/
│   │
│   ├── payments/                 # orchestrator
│   │   ├── domain/               #   Payment, state machine, saga steps, limits rules
│   │   ├── app/                  #   Deposit, Withdraw, Transfer, Resolve + ports.go
│   │   └── adapter/              #   grpc/, postgres/ (incl. outbox), redis/ (locks), rabbitmq/
│   │
│   ├── mpesa/                    # Daraja integration
│   │   ├── domain/               #   Provider request/result types, callback validation rules
│   │   ├── app/                  #   StkPush, B2C, QueryStatus, HandleCallback + ports.go
│   │   └── adapter/              #   grpc/, http/ (callback ingress), daraja/ (client), redis/, postgres/
│   │
│   ├── worker/
│   │   ├── notify/               #   event consumer → SMS/push (provider adapter inside)
│   │   └── recon/                #   intraday + daily reconciliation jobs
│   │
│   └── platform/                 # shared technical building blocks (no business rules)
│       ├── money/                #   Money value object, KES helpers, E.164 phone normaliser
│       ├── config/               #   env loading and validation
│       ├── obs/                  #   slog setup, OpenTelemetry, Prometheus metrics
│       ├── pg/                   #   pgx pool, tx helper, lock-ordering helper
│       ├── redisx/               #   client, distributed lock, rate limiter
│       ├── rmq/                  #   publisher/consumer with retry + DLQ
│       ├── outbox/               #   transactional outbox writer + relay
│       ├── idempotency/          #   key store + middleware/interceptor
│       ├── authn/                #   PASETO verify, service identity (mTLS) helpers
│       └── grpcx/                #   server/client defaults: deadlines, retries, status mapping
│
├── proto/                        # source of truth for internal contracts (buf module)
│   ├── buf.yaml
│   ├── buf.gen.yaml
│   └── nanga/
│       ├── identity/v1/identity.proto
│       ├── ledger/v1/ledger.proto
│       ├── payments/v1/payments.proto
│       ├── mpesa/v1/mpesa.proto
│       └── events/v1/events.proto      # RabbitMQ event payloads
├── gen/                          # generated Go code from proto (committed; CI checks no drift)
│
├── db/
│   ├── migrations/               # golang-migrate, one folder per schema
│   │   ├── identity/
│   │   ├── ledger/
│   │   ├── payments/
│   │   ├── mpesa/
│   │   └── recon/
│   └── queries/                  # sqlc input, one folder per schema
│       ├── identity/
│       ├── ledger/
│       ├── payments/
│       ├── mpesa/
│       └── recon/
│
├── deploy/
│   ├── Dockerfile                # single multi-stage file; ARG SERVICE selects cmd/<service>
│   ├── docker-compose.yaml       # Postgres, Redis, RabbitMQ + all services for local dev
│   └── k8s/                      # added in Phase 5
│       ├── base/                 #   one manifest set per deployable
│       └── overlays/{staging,production}/
│
├── test/
│   ├── e2e/                      # compose-based flows: deposit → transfer → withdraw → recon
│   └── daraja-sim/               # tiny fake Daraja: delays, dropped/duplicate/forged callbacks
│
├── .github/workflows/
│   ├── ci.yml                    # lint, test -race, sqlc/buf drift check, build, scan
│   └── release.yml               # build images, push, deploy (manual approval for production)
│
├── .env.example
├── .gitignore
├── .golangci.yml                 # includes import-boundary rules (depguard)
├── go.mod
├── go.sum
├── Makefile
├── sqlc.yaml                     # one entry per schema
├── README.md                     # setup, run, test in under 10 lines
├── spec.md
└── project-structure.md
```

## 3. Module Layout Rules

Each business module (`identity`, `ledger`, `payments`, `mpesa`) follows the same shape:

| Folder | Contains | May import |
|---|---|---|
| `domain/` | Entities, value objects, state machines, domain errors. Pure Go. | standard library, `platform/money` |
| `app/` | Use cases and `ports.go` (interfaces for repositories, publishers, clients). | `domain`, ports it declares |
| `adapter/` | gRPC/HTTP handlers and implementations of ports (Postgres, Redis, RabbitMQ, Daraja). | `app`, `domain`, `platform`, `gen` |

**Enforced boundaries** (via `depguard` in `.golangci.yml`):

1. `domain` imports nothing outside the standard library and `platform/money`.
2. `app` never imports `adapter`, `gen`, or any driver (`pgx`, `redis`, `amqp`, `gin`).
3. A module never imports another module's packages. Cross-module calls go through gRPC (`gen`) or events.
4. `platform` never imports business modules.
5. Only `cmd/*` wires adapters to use cases.

## 4. Conventions

- **One schema per module** inside a single local/dev PostgreSQL instance; separate databases or clusters per service become a deployment decision later, with no code change.
- **Naming:** gRPC packages `nanga.<service>.v1`; event routing keys `<aggregate>.<event>`; migrations `NNNNNN_description.{up,down}.sql`.
- **Tests:** unit tests beside the code (`*_test.go`); integration tests in the same package behind `//go:build integration` using Testcontainers; cross-service flows only in `test/e2e`.
- **Generated code:** `gen/` (proto) and each adapter's `postgres/sqlcgen/` are generated by `make gen`, never edited by hand.
- **Config:** environment variables only, validated at startup; secrets come from the secrets manager in production.

## 5. Makefile Targets

| Target | Action |
|---|---|
| `make up` / `make down` | Start/stop local stack (Postgres, Redis, RabbitMQ + services) |
| `make gen` | Run `buf generate` and `sqlc generate` |
| `make migrate` | Apply migrations for all schemas |
| `make test` | Unit tests with `-race` |
| `make test-int` | Integration tests (`-tags=integration`) |
| `make e2e` | Full-stack flows against compose and `daraja-sim` |
| `make lint` | `golangci-lint`, `buf lint`, `buf breaking` |
| `make build SERVICE=ledger` | Build one binary or image |

## 6. Deliberately Excluded

To keep the repo manageable by one person, the following are **not** included until a concrete need appears:

- Separate repositories per service or a separate `proto` repository.
- Per-service `go.mod` files, `go.work`, or a `pkg/` public-library folder.
- A service mesh, API-gateway product, or separate BFFs (Gin gateway is enough).
- Helm charts (Kustomize or plain manifests suffice at this scale).
- Separate `docs/`, ADR, or `scripts/` folders (documentation lives in the two root markdown files; scripts live in the Makefile).
- Kafka/NATS or a second message broker, and a standalone admin UI (admin actions are gRPC/CLI-driven at first).

## 7. When to Split Further

Split a package out of a grouped deployable into its own `cmd/<name>` only when one of these is true:

- It needs to **scale independently** (e.g. `recon` jobs starving `notify`).
- It requires a **different security boundary** (e.g. KYC document handling separated from `identity` credentials).
- Its **release cadence** differs enough that coupled deploys cause real pain.

Because modules already communicate through gRPC and events with no shared internals, the split is a new `cmd/` entry, a Kubernetes manifest and a compose service, not a refactor.

## 8. Build Order

| Phase | Create |
|---|---|
| 0 | `platform/{money,config,obs,pg,outbox,idempotency}`, `proto/`, `Makefile`, `deploy/docker-compose.yaml`, `ci.yml` |
| 1 | `ledger` module + `db/{migrations,queries}/ledger` + invariant and concurrency tests |
| 2 | `identity`, `gateway`, `platform/{authn,redisx,grpcx}` |
| 3 | `mpesa`, `payments` (deposit flow), `daraja-sim`, `worker/recon` (intraday) |
| 4 | Withdrawals, transfers, limits, `worker/notify`, `rmq` consumers |
| 5 | `test/e2e`, `deploy/k8s`, `release.yml`, daily reconciliation |
