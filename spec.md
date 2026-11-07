# Nanga — System Specification

| | |
|---|---|
| **Version** | 0.1 (Draft) |
| **Market** | Kenya (KES, M-Pesa via Safaricom Daraja) |
| **Style** | Hexagonal (ports & adapters) microservices |
| **Stack** | Go · Gin · PostgreSQL · Redis · RabbitMQ · gRPC |

---

## 1. Purpose & Scope

Nanga is a distributed payments and wallet orchestrator for Kenya. It lets customers hold a KES wallet, deposit and withdraw through M-Pesa, and transfer funds, while keeping an exact, auditable record of every shilling.

**In scope (v1):** customer onboarding and KYC, KES wallets, M-Pesa C2B/STK Push deposits, B2C withdrawals and payouts, wallet-to-wallet transfers, immutable double-entry ledger, reconciliation, notifications.

**Out of scope (v1):** multi-currency FX, card issuing, lending, bank rails (PesaLink/RTGS). The data model stays currency-aware so these can be added later.

## 2. Guiding Principles

1. **Correctness over speed.** A delayed payment is recoverable; a wrong balance is not.
2. **The ledger is the source of truth.** Balances are derived from, and reconcilable to, ledger entries.
3. **Callbacks are an optimisation, never a guarantee.** Every payment state can be reached by polling.
4. **Zero trust.** No inbound webhook, client claim or internal call is trusted without verification.
5. **Idempotent by construction.** Every state-changing command is safe to retry.
6. **Domain isolation.** Business logic never imports a framework, driver or SDK (hexagonal rule).
7. **Make the unsafe path impossible**, rather than relying on developer discipline (lint rules, DB constraints, restricted types).

## 3. Architecture

### 3.1 System Context

```
 Mobile / Web ──HTTPS──► API Gateway (Gin, REST/JSON)
                               │ gRPC (mTLS)
      ┌──────────┬─────────────┼──────────────┬──────────────┐
      ▼          ▼             ▼              ▼              ▼
  Identity   Customer/KYC   Wallet      Payments        Notification
  Service     Service       Service    Orchestrator       Service
                               │            │ gRPC
                               ▼            ▼
                           Ledger ◄──── M-Pesa Adapter ◄──► Safaricom Daraja
                           Service            ▲
                               ▲              │ callbacks (public edge)
                               └── RabbitMQ (events) ──► Reconciliation Service
```

### 3.2 Service Catalogue

| Service | Responsibility | Owns (PostgreSQL schema) |
|---|---|---|
| **gateway** | Public REST edge (Gin): authN, rate limiting, request validation, REST→gRPC translation. No business logic. | none |
| **identity** | Credentials, sessions, tokens, roles/permissions. | `identity` |
| **customer** | Profile, KYC state, limits tier, consent records. | `customer` |
| **wallet** | Wallet lifecycle, holds/reservations, available balance view. | `wallet` |
| **ledger** | Append-only double-entry journal; posting API; balance projections. | `ledger` |
| **payments** | Orchestrates deposits, withdrawals, transfers as sagas; transaction state machine; idempotency. | `payments` |
| **mpesa-adapter** | Only service that talks to Daraja: OAuth, STK Push, B2C, status query, callback ingestion. | `mpesa` |
| **notification** | SMS/push/email delivery from events. | `notify` |
| **reconciliation** | Daily and intraday matching of ledger vs. Safaricom statements; raises discrepancies. | `recon` |

**Rules:** one database schema per service, no cross-schema queries or foreign keys. Services integrate only through gRPC (synchronous) or RabbitMQ events (asynchronous).

### 3.3 Hexagonal Layout (per service)

```
service/
├── cmd/                  # entrypoint, wiring (dependency injection)
├── internal/
│   ├── domain/           # entities, value objects, state machines (pure Go)
│   ├── application/      # use cases; depends only on ports
│   ├── ports/
│   │   ├── in/           # use-case interfaces
│   │   └── out/          # repository, publisher, gateway interfaces
│   └── adapters/
│       ├── in/grpc/      # gRPC server handlers
│       ├── out/postgres/ # repositories (sqlc-generated queries)
│       ├── out/redis/    # locks, idempotency, cache
│       ├── out/rabbitmq/ # publisher / consumer
│       └── out/daraja/   # (mpesa-adapter only)
├── migrations/
└── test/                 # integration tests (Testcontainers)
```

Dependency direction is strictly inward: `adapters → application → domain`. This is enforced in CI with an import-boundary check (e.g. `go-arch-lint`).

### 3.4 Technology Choices

| Concern | Choice | Notes |
|---|---|---|
| Edge HTTP | Gin | Gateway only; internal traffic is gRPC. |
| Internal RPC | gRPC + Protocol Buffers | Shared `proto/` repo, versioned, linted with `buf`. |
| Database | PostgreSQL 16 | `sqlc` for type-safe queries; `golang-migrate` for migrations. |
| Cache / locks | Redis | Idempotency keys, distributed locks, rate limits, callback tokens. |
| Messaging | RabbitMQ | Durable topic exchanges, quorum queues, dead-letter queues. |
| Config | Environment + secrets manager | No secrets in images or repos. |
| Observability | OpenTelemetry → Jaeger/Tempo, Prometheus, structured JSON logs | |

## 4. Money Design Principles

### 4.1 Representation

- Amounts are **`BIGINT` minor units** (cents; KES has 2 decimals) paired with an ISO 4217 `currency` code. Floats are forbidden anywhere in the money path.
- A single `Money{Amount int64, Currency string}` value object in the domain layer is the only way to represent value. It rejects mixed-currency arithmetic and overflow.
- Display formatting (locale, separators, decimal places per currency) is a presentation concern and is never stored.
- Phone numbers are normalised to **E.164 without symbols** (`2547XXXXXXXX` / `2541XXXXXXXX`) at the gateway before any lookup or indexing, covering `07…`, `01…`, `+254…` and spaced inputs.

### 4.2 Double-Entry Ledger

- Every financial event is a **journal** of two or more **entries**, where `SUM(debits) = SUM(credits)` per currency. This is enforced in application code and by a deferred database constraint.
- The ledger is **append-only**. Rows are never updated or deleted (enforced by revoking `UPDATE/DELETE` privileges and a trigger). Corrections are made with reversing journals that reference the original.
- Each journal carries a unique `idempotency_key` and a `source_ref` (e.g. payment ID), so reposting the same event is a no-op.
- Balances live in a `ledger_balances` projection updated in the same transaction as the entries. A background verifier recomputes balances from entries and alerts on drift.

```
Deposit KES 1,000 via M-Pesa
  DR  mpesa_clearing (asset)        100000
  CR  wallet:{customer_id} (liability)  100000
```

**Chart of accounts (v1):** `mpesa_clearing`, `mpesa_float`, `fees_revenue`, `suspense`, `wallet:{id}` per customer. The `suspense` account holds unmatched or in-doubt funds until reconciled.

### 4.3 Concurrency & Consistency

- Money-moving writes run in a single PostgreSQL transaction using `SELECT … FOR UPDATE` on affected balance rows.
- **Deterministic lock ordering:** always lock the lower account ID first, which eliminates deadlocks on simultaneous opposite transfers.
- Wallets also carry a `version` column for optimistic concurrency on non-ledger updates; stale writes return `ABORTED`.
- Every database call uses a context timeout. Debits that would take `available_balance` below zero are rejected by a `CHECK` constraint as well as by application logic.
- Cross-service workflows use the **saga pattern**: the Payments service is an orchestrator that persists each step and runs compensating actions on failure. Event publication uses the **transactional outbox**, so a state change and its event commit atomically.

### 4.4 Transaction State Machine

```
CREATED → PENDING_PROVIDER → AWAITING_CALLBACK → COMPLETED
                │                   │
                └──► FAILED ◄───────┤
                                    └──► TIMED_OUT → (status query) → COMPLETED | FAILED
```

- Transitions are explicit, validated in the domain, and recorded in `payment_events` (append-only).
- States distinguish **pending**, **posted** and **settled** so clients never present "sent" as "received".
- Terminal states are immutable. Wallet funds for withdrawals are **held** at `CREATED` and released or captured on the outcome.

### 4.5 Idempotency

| Layer | Mechanism |
|---|---|
| Client → Gateway | Required `Idempotency-Key` header (UUIDv4) on all `POST` money endpoints. |
| Gateway / Payments | Redis `SET NX` lock with bounded lease (5 min) to absorb double-taps; durable record `(key, request_hash, response)` in PostgreSQL. Same key + different payload → `409`. |
| Payments → Daraja | Internal `transaction_id` is sent as `OriginatorConversationID` so Safaricom can reject duplicate retries. |
| Ledger | Unique `idempotency_key` per journal. |
| Consumers | Processed-message table keyed by event ID; handlers are idempotent. |

### 4.6 Limits & Controls

- Per-transaction, daily and monthly limits by KYC tier, enforced in the Customer/Payments services.
- Velocity checks (count and value over a window) via Redis counters; breaches go to a manual review queue.
- Step-up authentication (PIN or OTP) is required for withdrawals, new payees and amounts above a configured threshold.
- A mandatory **review step** (quote → confirm) is required for transfers: `POST /transfers/quotes` returns fees and the final amount; `POST /transfers` confirms it.
- A global **kill switch** per rail (e.g. disable B2C) is available through configuration with audit logging.

### 4.7 Reconciliation

- **Intraday:** every payment in `AWAITING_CALLBACK` is compared against Daraja status queries.
- **Daily:** Safaricom statements are matched to ledger journals by M-Pesa receipt number. Statuses: `MATCHED`, `MISSING_IN_LEDGER`, `MISSING_AT_PROVIDER`, `AMOUNT_MISMATCH`.
- Discrepancies are posted to `suspense` (never silently adjusted), alert on-call, and require a dual-approved resolution journal.

## 5. M-Pesa (Daraja) Integration

All Daraja traffic is confined to `mpesa-adapter`. The domain sees a `PaymentProvider` port; Daraja is one adapter.

### 5.1 Flows

| Flow | Daraja API | Result handling |
|---|---|---|
| Deposit (prompt) | STK Push (M-Pesa Express) | Callback or status query → post ledger |
| Deposit (paybill/till) | C2B validation + confirmation | Confirmation → post ledger |
| Withdrawal / payout | B2C | Result + timeout callbacks → post or reverse hold |
| Verification | Transaction Status Query | Authoritative fallback |

### 5.2 Callback Security (Anti-Spoofing)

1. **Single-use callback tokens:** each request is issued a random token in the callback URL (`/mpesa/callback/{token}`), stored in Redis with a TTL. Unknown or reused tokens are rejected with no detail.
2. **Network allow-listing:** callback routes accept only Safaricom's published production IP ranges, enforced at the gateway/WAF. This is defence-in-depth and must not be the only control; the ranges are config-managed and reviewed regularly.
3. **TLS:** valid CA-issued certificate, minimum TLS 1.2, TLS 1.3 preferred. (Confirm Safaricom's supported versions before enforcing 1.3-only.)
4. **Payload validation:** callbacks are matched against the stored request (amount, MSISDN, `CheckoutRequestID`/`ConversationID`). A mismatch is logged as a security event and never posted.
5. **Store first, process later:** the raw callback is persisted and acknowledged immediately; processing happens asynchronously from the persisted record.

### 5.3 Resolving In-Doubt Payments

- STK Push expires on the handset in roughly 20–30 seconds. Payments moves the record to `AWAITING_CALLBACK` and releases the request thread.
- A worker pool polls records that have waited longer than 60 seconds via the Transaction Status Query API, with exponential backoff and jitter, until a terminal state or a maximum age (then → `suspense` + alert).
- **Completion rule:** the ledger is updated only on (a) a validated callback with `ResultCode = 0`, or (b) a status query that explicitly returns success. Absence of a callback is never treated as failure or success.
- B2C timeouts are treated as **unknown**, not failed: funds stay held until status is confirmed. Blind retries are forbidden.

### 5.4 Provider Resilience

OAuth token cached in Redis with early refresh; per-call timeouts; bounded retries (idempotent calls only); circuit breaker around Daraja with a degraded-mode response ("service temporarily unavailable, you have not been charged") and an alert.

## 6. Security

### 6.1 Identity & Access

- **Passwords/PINs:** hashed with Argon2id (or bcrypt cost ≥ 12); never logged; constant-time comparison.
- **Access tokens:** short-lived (≤ 15 min) PASETO v4 tokens, validated at the gateway. **Refresh tokens:** opaque, random 256-bit, stored only as SHA-256 hashes, rotated on use with reuse detection.
- **Authorisation:** permission-based (e.g. `wallets:read`, `payments:create`) plus object ownership checks. Users may act only on resources they own.
- **Service-to-service:** mutual TLS and a service identity on every gRPC call; internal services are not publicly reachable.
- **Admin access:** separate identity realm, MFA mandatory, least privilege, four-eyes approval for manual ledger actions.

### 6.2 Edge Protection

- Rate limiting at the gateway: per-IP and per-account token buckets in Redis, with stricter limits on login, OTP and money endpoints.
- Strict input validation (unknown JSON fields rejected, body size limits, typed enums); parameterised queries only.
- Panic recovery middleware, request timeouts, request IDs, CORS allow-list, security headers, and generic error messages that never leak internals.

### 6.3 Data Protection

- TLS 1.2+ everywhere; PostgreSQL and Redis encrypted in transit and at rest.
- Sensitive fields (national ID, KYC documents) use application-level envelope encryption with keys in a KMS/secrets manager; keys are rotated.
- MSISDN and PII are masked in logs and UI (`2547****678`); card-style full-value display is never required.
- Compliance with the Kenya **Data Protection Act, 2019**: documented lawful basis and consent, data-subject access/erasure workflows (subject to financial record-retention duties), breach notification process, data residency review.
- AML/KYC obligations (tiered KYC, sanctions/PEP screening, suspicious-transaction reporting, record retention) are implemented as configurable policy. **Regulatory scope (CBK/NPS licensing, FRC reporting) must be confirmed with legal counsel before launch.**

### 6.4 Audit & Detection

- Immutable audit log (who, what, when, from where) for authentication events, limit changes, manual adjustments and admin actions.
- Alerts on ledger imbalance, balance drift, callback validation failures, abnormal velocity, and repeated auth failures.

## 7. API Design

### 7.1 Public REST (via Gin gateway)

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/auth/register`, `/v1/auth/login`, `/v1/auth/refresh` | Account access |
| GET | `/v1/wallets/{id}` | Wallet and balances (`ledger`, `available`, `held`) |
| GET | `/v1/wallets/{id}/transactions` | Paginated history (cursor-based) |
| POST | `/v1/deposits/mpesa` | Initiate STK Push |
| POST | `/v1/withdrawals/mpesa` | Initiate B2C withdrawal |
| POST | `/v1/transfers/quotes` · `/v1/transfers` | Quote, then confirm |
| GET | `/v1/payments/{id}` | Poll transaction status |
| POST | `/internal/mpesa/callback/{token}` | Daraja callbacks (network-restricted) |
| GET | `/healthz` · `/readyz` | Liveness and readiness |

### 7.2 Conventions

- Versioned paths; JSON envelope; amounts as `{"amount": 100000, "currency": "KES"}` in minor units.
- Errors return a stable machine code, a plain-language message and a recommended action, for example `INSUFFICIENT_FUNDS` with the shortfall, not "something went wrong".
- Status fields use the enumerated state machine in §4.4, so clients can show accurate "pending / completed / failed" messaging.
- List endpoints use cursor pagination, validated filters and bounded page sizes.

### 7.3 Internal gRPC

- Contracts in a shared `proto/` module, one package per service (`nanga.ledger.v1`, …). Breaking changes require a new major package; fields are only added, never reused or renumbered (backward/forward compatible).
- Every call carries a **deadline**; servers honour context cancellation.
- Errors use `google.golang.org/grpc/status` with typed details (`ErrorInfo`), mapped to HTTP at the gateway.

### 7.4 Events (RabbitMQ)

Topic exchange `nanga.events`; routing keys such as `payment.completed`, `ledger.journal.posted`, `kyc.approved`. Events are versioned, carry `event_id`, `occurred_at`, `correlation_id`, and are published via the outbox. Consumers acknowledge only after durable processing, retry with backoff, and dead-letter after the retry budget.

## 8. Resilience

| Concern | Approach |
|---|---|
| Timeouts | Deadline on every gRPC, DB, Redis and HTTP call. |
| Retries | Bounded, exponential backoff with jitter; only on idempotent operations and transient status codes (`UNAVAILABLE`). |
| Circuit breaking | On Daraja and any non-critical downstream. |
| Backpressure | Bounded worker pools and queue prefetch limits. |
| Graceful shutdown | Trap `SIGINT`/`SIGTERM`; stop accepting traffic, drain in-flight requests and workers, close pools. |
| Connection pools | Tuned `MaxOpenConns`/`MaxIdleConns` per service; PgBouncer where needed. |
| Failure of Redis | Idempotency falls back to the PostgreSQL unique record; limits fail closed for money endpoints. |

## 9. Observability

- **Traces:** OpenTelemetry across gateway → gRPC → DB → RabbitMQ, with `correlation_id` propagated through events.
- **Metrics (Prometheus):** request rate/latency/errors, payment success rate by flow, time-in-`AWAITING_CALLBACK`, callback-to-status-query ratio, queue depth, DLQ size, ledger drift, reconciliation exceptions.
- **Logs:** structured JSON, request ID and trace ID on every line, PII masked.
- **SLOs (initial):** API availability 99.9%; p95 gateway latency < 500 ms (excluding provider calls); 99% of M-Pesa deposits reach a terminal state within 2 minutes; ledger drift = 0.

## 10. Testing Strategy

| Level | Scope |
|---|---|
| Unit | Domain rules, `Money`, state machines, ledger invariants (property-based tests that debits = credits). Ports mocked. |
| Integration | Real PostgreSQL, Redis, RabbitMQ via Testcontainers; concurrency tests (parallel opposite transfers, duplicate idempotency keys, lost callbacks). |
| Contract | `buf breaking` on protos; consumer-driven contract checks for events. |
| Provider | Daraja sandbox plus a local simulator that injects delays, dropped callbacks, duplicates and forged payloads. |
| End to end | Docker Compose stack covering deposit → transfer → withdrawal → reconciliation. |
| Security | Dependency and image scanning, SAST, secret scanning, authorisation tests for object ownership. |
| CI gates | `go vet`, `staticcheck`, `go test -race ./...`, coverage threshold on domain/application layers, import-boundary check. |

## 11. Deployment & Operations

- **Containers:** multi-stage Dockerfiles, static Go binaries, distroless non-root images, read-only root filesystem.
- **Orchestration:** Kubernetes with liveness/readiness probes, resource limits, `NetworkPolicy` default-deny, and an ingress with TLS (cert-manager).
- **Pipeline:** GitHub Actions: lint → test (with service containers) → build → scan → push → deploy. Migrations run as a gated pre-deploy job and are backward-compatible (expand/contract).
- **Release:** rolling updates by default; canary for payments and mpesa-adapter.
- **Environments:** `local` (Compose), `staging` (Daraja sandbox), `production` (isolated accounts and secrets).
- **Data:** PostgreSQL HA with PITR backups, restore drills, and retention aligned to regulatory requirements.

## 12. Delivery Roadmap

| Phase | Deliverable |
|---|---|
| **0 – Foundations** | Repo layout, protos, CI, local Compose stack, shared libraries (`money`, `idempotency`, `outbox`, `observability`). |
| **1 – Ledger & Wallet** | Ledger service, wallet service, concurrency and invariant test suite. |
| **2 – Identity & Gateway** | Registration, auth, rate limiting, permissions, E.164 normalisation. |
| **3 – M-Pesa Deposits** | STK Push, callback security, status-query worker, reconciliation (intraday). |
| **4 – Withdrawals & Transfers** | B2C with holds, quote/confirm flow, limits and velocity checks. |
| **5 – Hardening** | Daily reconciliation, audit, load and chaos testing, security review, go-live runbooks. |

## 13. Open Questions

1. Licensing and regulatory perimeter (e-money issuer vs. agent of a licensed partner).
2. Paybill/till (C2B) vs. STK-only for v1.
3. KYC provider and IPRS/ID verification integration.
4. Fee model and revenue accounts.
5. Data residency requirements for hosting.

## Appendix A — Source Principles Applied

| Source | Applied in |
|---|---|
| *gRPC Microservices in Go* | Hexagonal layout (§3.3), gRPC contracts and versioning (§7.3), saga orchestration (§4.3), timeout/retry/circuit-breaker patterns (§8), mTLS (§6.1), Testcontainers and E2E testing (§10), Kubernetes deployment and OpenTelemetry (§9, §11). |
| *Let's Go Further* | Optimistic concurrency, query timeouts (§4.3), rate limiting (§6.2), hashed stateful tokens and permissions (§6.1), graceful shutdown and background-task draining (§8), structured logging and panic recovery (§6.2, §9), migrations and Makefile/CI quality control (§10, §11). |
| Simple Bank course outline | Deadlock avoidance via ordered locking (§4.3), sqlc + migrations (§3.4), table-driven and mock-based tests (§10). |
| Daraja engineering notes | Callback anti-spoofing, deterministic status querying, `OriginatorConversationID` idempotency, E.164 normalisation, immutable double-entry ledger (§4, §5). |
| Fintech design-system guidance | Distinct pending/posted/settled states, educational error messages, mandatory review step, idempotent action states, restricted/typed inputs, region-aware features (§4.4, §4.6, §7.2). |
