# Lending App

A Nigerian fintech lending API in Go: user onboarding with KYC, data-derived
credit scoring, amortized loans with server-side pricing, a real repayment
ledger, collections, tamper-evident audit, and versioned key rotation.
Standard library-first (`net/http`, `database/sql`), Postgres + Redis.

```text
clients
  │  HTTPS (terminated at the load balancer)
  ▼
middleware chain ─ request ID → access log → security headers → rate limit → auth
  ▼
handlers (thin: decode, validate, call service, audit)
  ▼
services (domain rules, transactions, retries)
  ▼
repositories (SQL, tx-aware) ──► Postgres 18 (system of record)
  └─► Redis 8 (rate limiting only — sessions live in Postgres)
```

## Contents

- [Architecture](#architecture)
- [Domain model](#domain-model)
- [Security](#security)
- [Key rotation](#key-rotation)
- [Production readiness](#production-readiness)
- [Getting started](#getting-started)
- [API overview](#api-overview)
- [Testing](#testing)
- [Operator checklist](#operator-checklist)

## Architecture

```
backend/
  cmd/api/            main.go (bootstrap, config, graceful shutdown)
                      rekey.go (PII re-encryption job)
  internal/
    handlers/         HTTP adapters — one file per resource + helpers.go
                      (body caps, strict JSON, audit metadata, error mapping)
    services/         domain logic — auth, loans, credit, repayments,
                      collections, admin, audit
    repositories/     SQL — tx.go (tx propagation), db.go (Transact with
                      retry, advisory locks), one file per aggregate +
                      refresh/password-reset stores
    domain/entities/  types + state machines + allowlists
    middleware/       auth, admin, rate limit, security headers,
                      request ID, access log
    infrastructure/   JSON structured logger
  pkg/
    auth/             JWT service (access + refresh, rotation-aware)
    crypto/           versioned AES-GCM keyring + HMAC index keys
    retry/            retriable-error detection + jittered backoff
    validator/        email/phone/BVN/NIN/password rules
  migrations/         8 goose migrations (constraints live here, not just code)
```

Request flow for `POST /api/v1/loans/{id}/repay`:

1. `RequestID` stamps `X-Request-ID` (propagated into audit rows).
2. `LoggerMiddleware` records method/path/status/latency (no query strings).
3. `SecurityMiddleware` sets HSTS, CSP, `no-store`, COOP/CORP… headers.
4. `RateLimiter` runs an atomic Lua fixed-window check in Redis
   (fail-open if Redis is down; `X-Forwarded-For` trusted only when
   `TRUST_PROXY=true`).
5. `AuthMiddleware` validates the JWT **and** re-checks `IsActive` + current
   role from Postgres, so disables and role changes bite immediately.
6. Handler decodes (1 MiB cap, JSON only), service runs a **serializable**
   transaction with automatic retry, audit is appended (best-effort `_`).

## Domain model

### Loan lifecycle (enforced, not aspirational)

```
pending → under_review → approved → disbursed → repaid
              │              │            └────→ defaulted → (repaid)
              └────→ declined ◄──┘
```

`entities.CanTransitionLoan` gates every change; the DB `CHECK` rejects
anything outside the vocabulary; `UpdateStatusConditional` makes concurrent
transitions safe. Key detail: the schedule is created at **disbursement**,
not approval — interest must never accrue pre-funding. `totalPayment` is
reconciled to the **sum of schedule rows**, and every loan carries a
fee-inclusive `effectiveApr` for disclosure.

Borrowers do not self-price: `PricedRate` maps risk bands to rates
(≥740 → 12%, ≥670 → 18%, ≥580 → 24%, else 30%).

### Repayment ledger

Each payment allocates **late fee → interest → principal** across the
earliest-due installments (`SELECT … FOR UPDATE` inside a serializable tx
serialized per-loan by an advisory lock). Every application records
`amountPaid / principalPaid / interestPaid / feePaid`, so partials accumulate
instead of vanishing, overpayments are returned as `overpayment` for
refund/credit handling, and `transactionId` (unique, required) makes retries
idempotent — replaying a transaction is rejected, never double-applied.

### Credit scoring (100% data-derived)

No randomness. Each report is computed from live data:

| Field | Source |
|---|---|
| `paymentHistory` % | on-time ÷ due installments across funded loans |
| `creditUtilization` % | outstanding ÷ annual income (or disbursed), capped 0–100 |
| `totalDebt` | Σ `BalanceDue()` over unpaid installments |
| `availableCredit` | capacity − debt, floored at 0 |
| `creditAgeMonths` | account tenure |
| `numAccounts` / `hardInquiries` | loan count / applications in 12 months |
| `delinquentAccounts` | loans with past-due unpaid rows or `defaulted` |

Deterministic scoring (±60 payment history, utilization bands, −60 per
delinquent loan capped −180, inquiry/tenure/KYC/employment adjustments,
clamped 300–850). Regenerating without new activity returns the existing row
instead of spamming history. This is an *internal behavioral* score — a
licensed bureau remains the decision of record for regulated lending.

### Collections

Delinquency is **per-installment**, not loan maturity: a missed month-2
payment surfaces in days, with date-truncated DPD, one-time 5%-capped late
fees, `UNIQUE … WHERE status='active'` per loan, and rescan refresh of stale
snapshots. Assignment never wipes contact history.

## Security

- **Passwords**: bcrypt (72-byte cap enforced — bcrypt silently truncates
  beyond it), strength rules, generic login/registration errors (no user
  enumeration), password changes revoke all sessions.
- **Tokens**: 15-minute access JWTs (`jti`/`iss`/`aud`), 30-day rotating
  refresh tokens persisted in `refresh_tokens` — reuse detected and rejected,
  logout + logout-all supported.
- **PII at rest**: AES-256-GCM with random nonces; emails/phones/BVN/NIN
  encrypted, looked up via indexed HMAC-SHA256 columns (no table scans);
  decrypt failures propagate as errors, never silent `""`.
- **Audit**: append-only at the DB level (`UPDATE`/`DELETE` raise
  `audit_logs is append-only`), recursive PII redaction, actor vs. subject,
  request-ID correlation, dedicated `LOGIN_FAILED` / `REPAYMENT_FAILED` /
  `KYC_APPROVE` / `KYC_REJECT` actions.
- **Input**: body caps, content-type enforcement, allowlisted enums/roles/
  statuses, age ≥ 18 and 500-char purpose caps in the schema itself.

## Key rotation

The centerpiece. Rotating `ENCRYPTION_KEY` the naive way bricks every
encrypted row. This app rotates with **zero downtime and zero data loss**.

### The envelope

Stored values look like this:

```
v3f9a1c2e:q7B2mUzR4tX…(base64 nonce|ciphertext)
```

- `3f9a1c2e` is the **key ID**: 8 hex chars of `SHA-256("enc-key-id:" + key)`.
- Crucially, the ID is bound to the **key material, not a slot**. The same
  key keeps the same ID whether it is primary or previous.

> **Pitfall we actually hit (learn from it):** the first design used
> positional versions — old key = v1, new key = v2. The live drill proved it
> broken: after re-encryption, dropping the previous key renumbered the
> survivor from v2 back to v1, orphaning every rotation-window row
> (`unknown key version v2`, logins dead). Fingerprint-bound IDs make that
> class of bug impossible: identities never shift under you. Even on total
> mismatch, GCM authentication fails **closed** — never wrong plaintext.

Legacy unprefixed rows (pre-versioning) are tried against primary, then
previous. Unknown IDs are rejected loudly, never guessed.

### Worked example

Starting state — one key, everything written as `v<id-of-A>`:

```bash
# .env
ENCRYPTION_KEY=<key-A>          # primary → id 3f9a1c2e
```

```sql
SELECT left(email,12), left(email_hmac,8) FROM users;
--  v3f9a1c2e:q7… | 9be24c11
```

**Step 1 — introduce the new key.** Generate (`make key`), deploy with the
old key demoted:

```bash
ENCRYPTION_KEY=<key-B>          # primary → id 71bd04aa
ENCRYPTION_KEY_PREVIOUS=<key-A> # reads still served
```

Reads now try both keys (decrypt by envelope ID; email lookup tries primary
HMAC then previous HMAC). New writes use `v71bd04aa:…`. The app is fully
live throughout — old users log in, new users register:

```sql
SELECT DISTINCT left(email,11) FROM users;
--  v3f9a1c2e:…     (old rows)
--  v71bd04aa:…     (new rows)
```

Same pattern for `HMAC_KEY_PREVIOUS` and `JWT_SECRET_PREVIOUS` (JWT fallback
accepts the old secret for validation while signing with the new one; drop
it after the 30-day refresh TTL).

**Step 2 — re-encrypt.** Dry-run first (reports without writing), then live:

```bash
make reencrypt-dry   # → scanned=1250 rewritten=1198 skipped=52 failed=0
make reencrypt       # → scanned=1250 rewritten=1198 skipped=52 failed=0
```

The job (`api -reencrypt`) pages users in batches of 500 with pacing,
decrypts through the key ring (proving readability), rewrites every PII
field under the primary ID, and refreshes HMACs. It is **idempotent and
resumable** — a second run is a no-op (`rewritten=0 skipped=1250`) — and a
Postgres advisory lock refuses concurrent runs. Only counts are logged;
never plaintext, ciphertext, or key material.

```sql
SELECT DISTINCT left(email,11) FROM users;
--  v71bd04aa:…     (everything)
```

**Step 3 — retire the old key.** Remove `ENCRYPTION_KEY_PREVIOUS`, deploy.
Because IDs are material-bound, `key-B` is still `71bd04aa` — nothing
orphans. Verify with a login, then destroy the old key material.

### Rotation rules enforced in code

- Previous key identical to primary → boot rejected.
- JWT previous secret under 32 chars → boot rejected.
- `DB_SSLMODE=disable` in production → boot rejected.
- Unknown envelope key ID → error, never fallback guessing.

## Production readiness

- **Timeouts everywhere**: 8s query ceiling, 20s transaction ceiling, 8s PG
  `statement_timeout`, 5/3/3s Redis dial/read/write, 15/30/60s HTTP
  timeouts, 1 MiB body/header caps, 25s bounded collections scan.
- **Retry**: `pkg/retry` retries serialization failures (40001), deadlocks,
  lock timeouts, and connection drops with jittered backoff; startup
  connect loops ride through DB/Redis restarts.
- **ACID**: every multi-write path (`ApplyForLoan`, `Disburse`,
  `MakeRepayment`) runs in one **serializable** transaction via
  `repositories.Transact` — never bare `BeginTx`.
- **Concurrency**: `0x1e4d1_*` advisory locks give one migrator, one
  scanner, one rekey-er across N replicas; per-loan xact locks +
  `FOR UPDATE` serialize hot money rows (5 concurrent repays → applied
  exactly once each, verified live).
- **Failover**: Redis loss degrades rate limiting to fail-open (sessions are
  in Postgres, so nobody gets locked out); pools error instead of wedging;
  30s graceful drain on shutdown.
- **Ops**: pinned images (`golang:1.27-alpine`, `alpine:3.23`,
  `postgres:18-alpine`, `redis:8-alpine`), 30 MB non-root image with
  healthcheck, JSON logs with request IDs, health endpoint returns real
  503s, `MIGRATIONS_DIR` resolution that works from any working directory.

## Getting started

```bash
cp .env.example .env
make key   # twice → ENCRYPTION_KEY=… and HMAC_KEY=… (do NOT reuse one key)
# fill JWT_SECRET (openssl rand -base64 48), DB_PASSWORD, REDIS_PASSWORD

docker compose up -d --build   # postgres 18 + redis 8 + api :8080
curl localhost:8080/health

make migrate-install           # pinned goose CLI (v3.28.0)
make build && make vet
go test ./...
```

| Target | Purpose |
|---|---|
| `make build` / `make vet` | compile / static analysis |
| `make test` | full test suite with coverage |
| `make migrate-up` / `migrate-down` | apply / roll back one migration |
| `make reencrypt-dry` / `make reencrypt` | key-rotation dry-run / live run |
| `make key` | generate a base64-32B key |
| `make clean` | ⚠️ destroys volumes (dev only) |

## API overview

Public: `POST /auth/register|login|refresh|logout|forgot-password|reset-password`,
`GET /health`. Authed: profile, loans (+`{id}`, `/repay`, `/repayments`),
credit (`/score`, `/report`, `/refresh`), collections (+`/{id}/payment-arrangement`,
idempotency via `Idempotency-Key` header or `transactionId`).
Admin: dashboard, users (`/status`, `/role`, `/kyc/approve|reject`),
loans (`/status`, `/disburse`), collections (`/status`, `/assign`,
`/run-overdue-scan`).

Errors are `{"error": CODE, "message": …}`; internal details never leak
(unknown email → `invalid credentials`, infra failure → generic 500 so
monitoring can tell them apart).

## Testing

```bash
go test ./...                  # unit: money math, state machine, crypto rotation
```

Live drills used during development (disposable PG18 + Redis 8, destroyed
after): full money path register→disburse→repay, refresh-reuse rejection,
rate-limit 429s, 5-way concurrent repays, and the complete A→rotate→rekey→
drop-previous key cycle. Worth re-running before any major release.

## Operator checklist

Not codeable in this repo — do these before real money:

1. Real secrets, `ENV=production`, `DB_SSLMODE=require`, `TRUST_PROXY=true`.
2. Managed Postgres (PITR + restore drills) and Redis (persistence/failover).
3. Licensed bureau / open-banking provider as decision of record (+ consent).
4. KMS/HSM for data keys (rotation is restart-based today).
5. TLS termination, alerts (5xx/429/latency), cron for the overdue scan,
   supervised lending pilot.
