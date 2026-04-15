# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Project Is

Antimoney is a double-entry accounting web app (GnuCash-inspired). Full-stack: Go backend + React/TypeScript frontend, PostgreSQL database, deployable to GCP Cloud Run.

## Commands

### Full Stack (Docker Compose — recommended)
```bash
make up          # Start Postgres + backend + frontend
make down        # Stop all containers
make build       # Rebuild Docker images
make logs        # View container logs
make test        # Run backend + frontend tests
make e2e         # Run Playwright end-to-end tests
```

### Backend
```bash
cd backend
go test ./...                        # All tests
go test -v ./internal/gnc/           # Single package (example)
go test -cover ./...                 # With coverage
DATABASE_URL="postgres://antimoney:antimoney_dev@localhost:5432/antimoney?sslmode=disable" go run ./cmd/server/
```

### Frontend
```bash
cd frontend
npm run dev      # Dev server (http://localhost:5173)
npm run build    # Type-check + Vite build
npm run lint     # ESLint
npm run test     # Vitest with coverage
```

## Architecture

### Request Flow
```
Browser (React SPA) → /api/* → Go Chi Router → Service layer → pgx → PostgreSQL
```

- **Dev proxy**: Vite proxies `/api` and `/auth` to `localhost:8000`
- **Auth**: JWT tokens stored in localStorage; injected into requests via `src/api/client.ts`

### Backend (`backend/`)
- `cmd/server/main.go` — entry point; sets up router, runs migrations, seeds DB
- `internal/handlers/` — HTTP handlers (thin: parse request, call service, return JSON)
- `internal/services/` — business logic (TransactionService, AccountService, etc.)
- `internal/models/` — domain types (Account, Transaction, Split, User)
- `internal/gnc/` — rational number engine: all financial amounts stored as (numerator, denominator) pairs to avoid floating-point errors
- `internal/auth/` — JWT middleware; BookGUID and UserID injected into request context
- `internal/seed/` — default currencies and chart-of-accounts seeding
- `migrations/` — SQL migrations run automatically on startup via golang-migrate

### Frontend (`frontend/src/`)
- `api/client.ts` — all API calls go through here (JWT injection, error handling)
- `types/index.ts` — TypeScript interfaces mirroring backend JSON contracts
- `components/` — reusable UI (AccountTree, TransactionForm, Register, ReconcileWizard, etc.)
- `pages/` — routed page components (Dashboard, Accounts, AccountRegister, Transactions, DataManagement, LoginPage)
- `auth/` — auth context (token stored in localStorage)
- `i18n.ts` — English + Portuguese (pt-BR) translations

### Key Design Decisions
- **Rational numbers**: Financial amounts never use floats. The `gnc` package represents amounts as `{num, denom int64}`.
- **Aggregate transactions**: A transaction and all its splits are created/read atomically in a single DB transaction. Splits must sum to zero; an "Imbalance" account is auto-created if they don't.
- **OCC (Optimistic Concurrency Control)**: `version` column on rows; updates reject stale writes.
- **JSONB metadata**: Extensible `metadata` column on accounts/transactions instead of a KVP slots table.
- **Post-date normalization**: Transaction dates normalized to 11:00 UTC to prevent timezone drift.
- **Multi-tenancy**: Each user has a `book_guid`; all queries are scoped to it via request context.

## Security & Architecture Best Practices

- **Zero Trust & Least Privilege**: Components should operate with the minimum required permissions necessary. Apply strict multi-tenancy controls across the stack.
- **Data Protection**: Store secrets securely. Prevent logging of Sensitive Data (PII, credentials, tokens) in application logs or APM tools.
- **Authentication & Authorization**: Enforce strict JWT validation on all protected API endpoints. Ensure tokens are short-lived.
- **Input Validation**: Validate, sanitize, and strictly type-check all incoming input at the API boundaries to mitigate injection and malicious payloads.
- **Vulnerability Management**: Keep an OWASP Top 10 focus (e.g., IDOR prevention, CSRF, XSS protections).

## Gotchas & Non-Obvious Behavior

- **Transaction create vs. update**: `CreateTransaction` auto-balances with an Imbalance account if splits don't sum to zero. `UpdateTransaction` rejects imbalance — caller must balance before sending.
- **Update unreconciles splits**: All splits are deleted and re-inserted on update; `reconcile_state` resets to `'n'` for every split regardless of prior state.
- **Pagination is inconsistent**: Register (`/accounts/{id}/register`) uses cursor-based pagination (`cursor_date`, `direction`, `limit`). Global transactions use `limit`/`offset`. Don't mix the patterns.
- **Missing `cursor_date` loads all rows**: The register endpoint falls back to a full load if `cursor_date` is absent. Always provide it for paginated views.
- **i18n**: Every new translation key must appear in both `en` and `pt-BR` sections of `i18n.ts`.
- **Credit-normal accounts**: Liability/Credit/Income/Equity accounts flip the UI column labels (Increase/Decrease instead of Deposit/Withdrawal) but the underlying split signs are unchanged.
- **401 hard-reloads the app**: `client.ts` clears the JWT token and calls `window.location.reload()` on any 401. No retry or soft error recovery is possible.
- **Zero-value splits are silently dropped**: Splits where `value_num == 0` are removed during transaction validation and will not be persisted.
- **Placeholder accounts reject splits**: Splits targeting a placeholder account return `ErrPlaceholderAccount`. Placeholder accounts exist only for tree structure.
- **CORS in development**: The backend allows `localhost:*` origins. Production Cloud Run services communicate via internal VPC — the CORS config would need updating if origins change.

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | `postgres://antimoney:antimoney_dev@localhost:5432/antimoney?sslmode=disable` | PostgreSQL connection |
| `PORT` | `8000` | Backend port |
| `ENVIRONMENT` | `development` | `development` or `production` |
| `JWT_SECRET` | auto-generated | JWT signing secret |
