# Nanga

**Move money in Kenya with confidence.** Nanga is a payments and wallet platform that lets people hold Kenyan shillings, top up and cash out through M-Pesa, and send money to each other, with every shilling tracked and every payment safe to retry.

---

## What It Does

- **Wallets in KES:** a secure balance for every customer.
- **M-Pesa deposits:** pay in with an STK Push prompt on your phone.
- **M-Pesa withdrawals:** cash out to your M-Pesa number.
- **Transfers:** send money to another Nanga wallet, with a review step before you confirm.
- **Clear statuses:** payments show as *pending*, *completed* or *failed*, never an ambiguous "sent".
- **Plain-language errors:** if something fails, the app says why and what to do next.

## Why You Can Trust It

| Promise | How it's kept |
|---|---|
| **No lost or duplicated money** | Every payment is safe to retry; double-taps and network drops can't charge twice. |
| **Balances always add up** | An append-only double-entry ledger records every movement; nothing is ever overwritten. |
| **Missed M-Pesa notifications don't matter** | If a callback never arrives, Nanga asks Safaricom directly before deciding. |
| **Fake payment notices are rejected** | Callbacks are verified by single-use tokens, source checks and payload matching. |
| **Daily proof** | Our records are matched against Safaricom statements; any gap is flagged, not hidden. |

---

## Technical Overview

| | |
|---|---|
| **Language** | Go |
| **Edge API** | Gin (REST/JSON) |
| **Internal RPC** | gRPC + Protocol Buffers |
| **Data** | PostgreSQL (one schema per service) |
| **Cache / locks** | Redis |
| **Events** | RabbitMQ (transactional outbox) |
| **Architecture** | Hexagonal (ports & adapters), microservices in one monorepo |

```
Client ─► gateway (Gin) ─gRPC─► identity · ledger · payments ─► mpesa ─► Safaricom Daraja
                                          │
                                          └─ RabbitMQ ─► worker (notifications, reconciliation)
```

| Deployable | Role |
|---|---|
| `gateway` | Public REST edge: auth, rate limiting, validation |
| `identity` | Accounts, credentials, KYC tiers |
| `ledger` | Double-entry ledger and wallets |
| `payments` | Payment state machine and sagas |
| `mpesa` | The only service that talks to Daraja |
| `worker` | Notifications and reconciliation |

### Design Rules

- Money is stored as **integer minor units** plus currency code. No floats.
- Money-moving writes lock accounts in a **fixed order** to avoid deadlocks.
- Every state-changing request carries an **`Idempotency-Key`**.
- Payment results are confirmed by a valid callback **or** a status query, never assumed.
- Domain code never imports frameworks or drivers; boundaries are enforced by lint.

---

## Quick Start

**Prerequisites:** Go 1.22+, Docker, `make`, `buf`, `sqlc`, `golang-migrate`.

```bash
cp .env.example .env     # add Daraja sandbox credentials
make up                  # start Postgres, Redis, RabbitMQ and services
make migrate             # apply database migrations
make test                # unit tests (-race)
```

| Command | Purpose |
|---|---|
| `make gen` | Regenerate protobuf and sqlc code |
| `make test-int` | Integration tests (Testcontainers) |
| `make e2e` | Full flows against a simulated Daraja |
| `make lint` | Linters, import-boundary and proto checks |
| `make build SERVICE=ledger` | Build one service |

Health checks: `GET /healthz` and `GET /readyz` on every service.

## Documentation

- [`spec.md`](./spec.md): requirements, money and security design, M-Pesa integration.
- [`project-structure.md`](./project-structure.md): repository layout and conventions.

## Status

Draft, pre-release. Targets Kenya (KES, M-Pesa via Safaricom Daraja). Regulatory scope (CBK/NPS licensing, AML/KYC) is to be confirmed with legal counsel before launch.

## Security

Please report vulnerabilities privately to the maintainer rather than opening a public issue.
