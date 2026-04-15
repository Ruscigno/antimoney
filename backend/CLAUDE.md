# Backend CLAUDE.md

Go HTTP server using Chi router. PostgreSQL via pgx/v5. All financial amounts use the `gnc` rational number package — never floats.

## Commands

```bash
go test ./...                        # All tests
go test -v ./internal/gnc/           # Single package
go test -cover ./...                 # With coverage
DATABASE_URL="postgres://antimoney:antimoney_dev@localhost:5432/antimoney?sslmode=disable" go run ./cmd/server/
```

## Package Layout

- `cmd/server/main.go` — entry point: loads config, runs migrations, seeds DB, wires router
- `internal/handlers/` — thin HTTP handlers: parse → call service → write JSON
- `internal/services/` — all business logic (`TransactionService`, `AccountService`)
- `internal/models/` — domain structs (`Account`, `Transaction`, `Split`, `User`)
- `pages/` — routed page components (Dashboard, Accounts, AccountRegister, Transactions, DataManagement [JSON/CSV/GnuCash import], LoginPage)
- `internal/gnc/` — rational number engine
- `internal/auth/` — JWT middleware; injects `BookGUID` and `UserID` into context
- `internal/seed/` — seeds currencies and a default chart-of-accounts on startup
- `migrations/` — numbered SQL migrations run automatically via golang-migrate on startup

## Rational Numbers (`internal/gnc`)

**All financial amounts must use `gnc.Numeric`, never `float64`.**

- Amounts are stored as `(num int64, denom int64)` pairs. Example: $10.50 → `{1050, 100}`.
- Arithmetic: `Add`, `Sub`, `Mul`, `Div`, `Neg`, `Abs`.
- Rounding: `Convert(denom, mode)` with modes `RoundBanker`, `RoundTruncate`, `RoundFloor`, `RoundCeiling`, `RoundNever`. `RoundNever` returns `ErrRemainder` if the result is not exact — always handle this error.
- Denominator is always positive (sign lives in numerator only). `gnc.New(num, 0)` panics.
- `gnc.Sum()` uses `math/big` internally to avoid int64 overflow on large aggregations.

## Transaction Invariants

- **Splits must sum to zero** (all splits in a transaction, in `gnc.Numeric` terms).
- **Create** auto-balances: if splits don't sum to zero, an `Imbalance` account is auto-created and a balancing split is appended.
- **Update** does not auto-balance: returns `ErrUnbalancedTransaction` if splits don't sum to zero. The caller must balance before calling update.
- **Update deletes all old splits** and re-inserts new ones. All new splits start with `reconcile_state = 'n'`. There is no way to preserve reconcile state through an update.
- **Zero-value splits (`value_num == 0`) are silently dropped** during validation.
- **Placeholder accounts** reject splits — returns `ErrPlaceholderAccount`.
- **Post-date normalization**: `normalizePostDate()` sets the time component to 11:00 UTC regardless of input. The date is preserved; only the time is overwritten.

## Security & Architecture Best Practices

- **Authentication**: JWTs must be strictly validated for signature integrity and expiration (`JWT_SECRET` must be securely managed).
- **Authorization & IDOR Prevention**: Always enforce multi-tenancy rules. Use `book_guid` derived from the validated JWT to scope all data access. Never implicitly trust client-provided IDs for authorization.
- **SQL Injection Prevention**: Rely strictly on `pgx` parameterized queries or query builders. Never concatenate strings for SQL execution.
- **Rate Limiting & CORS**: Apply API rate limiting on critical endpoints (e.g., `/auth/login`). Restrict CORS in production to specific, trusted front-end origins (avoid `Access-Control-Allow-Origin: *`).
- **Structured SecOps Logging**: Log security-critical events (e.g., login failures, authorization rejections) safely without exposing raw passwords or tokens in standard out.

## Multi-Tenancy

Every service method must scope all queries to the user's book:

```go
bookGUID := auth.BookGUIDFromCtx(ctx)
```

**Never omit `book_guid` from WHERE clauses.** All data is isolated per book. The `BookGUID` is extracted from the JWT by `auth.RequireAuth` middleware and placed in context.

## Reconcile States

Split `reconcile_state` is a single character:

| Value | Meaning |
|---|---|
| `'n'` | Not reconciled (default) |
| `'c'` | Cleared (acknowledged, matches bank statement) |
| `'y'` | Reconciled (formally confirmed via wizard) |

`BatchReconcileSplits` sets state directly to `'y'` — it bypasses the `n → c → y` UI flow.

## Database Migrations

- Files live in `migrations/` as `NNNNNN_name.up.sql` / `NNNNNN_name.down.sql`.
- Migrations run automatically at startup via `database.RunMigrations()`.
- Use sequential 6-digit prefixes for new migrations (e.g., `000006_...`).
- **Never edit an existing migration** — always add a new one.

## Testing

Backend tests use **testcontainers** to spin up a real Postgres instance. There is no mocking of the database layer.

```go
// In test setup:
db, err := testutil.SetupDB(ctx, "../../migrations")
defer db.Teardown(ctx)
```

- `testutil.SetupDB` starts a `postgres:16-alpine` container, runs all migrations, and returns a `*pgxpool.Pool`.
- Tests are integration tests against a real schema — don't write unit tests that mock the DB.
- Pass the `migrationDir` relative path carefully; it's relative to the test file location.

## Handler Conventions

- Handlers are thin: decode request body → validate → call service → write JSON.
- Use `handlers.WriteErrorPublic(w, status, msg)` for errors, `handlers.WriteJSONPublic(w, status, v)` for success.
- Public routes (`/auth/*`, `/health`) use the `Public` helpers. Protected routes (`/api/*`) use the same helpers but are behind `auth.RequireAuth` middleware.
- The 30-second `middleware.Timeout` applies to all routes. Long-running operations should be bounded.

## Route Structure

```
/health              GET  — public health check
/auth/register       POST — create user + book
/auth/login          POST — returns JWT
/auth/me             GET  — validate token
/api/*               — all protected (RequireAuth middleware)
  /transactions      GET/POST
  /transactions/:id  GET/PUT/DELETE
  /transactions/splits/:id/toggle  PATCH
  /transactions/splits/reconcile   POST
  /accounts          GET/POST
  /accounts/:id      GET/PUT/DELETE
  /accounts/:id/register           GET (cursor-paginated)
  /accounts/:id/reconciled-balance GET
  /accounts/:id/reconcile          POST
  /data/import       POST
  /data/export       GET
  /books             GET
```
