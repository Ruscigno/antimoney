# PRD & Design — Plaid Bank Sync

- **Date:** 2026-06-08
- **Status:** Draft for review
- **Component:** Antimoney (Go backend + React/TS frontend)
- **Target bank (MVP):** RBC (Canada), via a Plaid Trial plan (production, ≤10 Items)

## 1. Problem & Summary

Antimoney users enter every transaction by hand. This feature lets a user connect a
bank through Plaid, link each bank account 1:1 to an Antimoney account, and pull new
transactions into a **GnuCash-style import-matcher overlay** — auto-categorizing where
possible and letting the user assign the split account where not — then post them as
double-entry transactions marked *cleared*. All Plaid secrets and the long-lived
`access_token` stay server-side and **encrypted at rest**.

## 2. Goals

1. Connect a bank via Plaid Link (in the browser) and map each bank account 1:1 to an
   Antimoney account. One Plaid connection (Item) can host several bank accounts; each
   maps to at most one Antimoney account.
2. Fetch new (deduplicated) transactions on **two triggers**: the first time a linked
   account's register is opened on a given day, and a manual **"Sync now"** button.
3. Give the user clear **feedback**: a "syncing…" status while it runs, a success count,
   and a generic error message on failure.
4. Present new transactions in an overlay that auto-categorizes when possible and lets
   the user pick the split (category) account otherwise; on confirm, post each as a
   transaction with `reconcile_state = 'c'` so it flows into the existing ReconcileWizard.
5. Keep `PLAID_SECRET` and `access_token` server-side; encrypt `access_token` at rest.

## 3. Non-Goals (out of scope for MVP)

- Plaid **webhooks** and **scheduled/cron** background sync (triggers are user-driven).
- A **category rules engine** or mapping Plaid's category taxonomy to accounts — but the
  categorizer is built behind an interface so these can be added later without rework.
- **Multi-currency display.** RBC is CAD; amounts import correctly (the `gnc` engine is
  currency-agnostic), but `formatCurrency()` remains BRL-labeled. Accepted limitation.
- Update-mode/"reconnect" niceties beyond a basic disconnect + reconnect (a sync against
  an item needing re-auth surfaces a "reconnect needed" message).

## 4. Architecture

Chosen approach: **frontend UX + the existing Go backend** (no new infrastructure, reuse
existing patterns). Only Plaid Link runs in the browser; the secret-bearing steps run on
the backend.

```
Browser                         Go backend (/data/plaid)                Plaid API
  Plaid Link  ── public_token ─► exchange (client_id+secret) ─────────► /item/public_token/exchange
  Connect UI                     encrypt+store access_token (DB)
  Matcher overlay ◄── suggestions ─ /transactions/sync (cursor) ◄────── /transactions/sync
  confirm ──────────────────────► CreateTransaction() → splits (cleared)
```

- **Frontend:** `react-plaid-link` for the Link flow; a **Connect bank** section on the
  existing `DataManagement` page; an **Import Matcher** overlay component reused by both
  sync triggers; all calls via `fetchJSON`.
- **Backend:** new `internal/plaid` package — a typed client over Plaid's REST API
  **behind an interface** (`PlaidClient`) for testing — plus `PlaidService` and
  `PlaidHandler` mounted at `/data/plaid` (mirrors the existing import/export handler).
- **Config:** add `PLAID_CLIENT_ID`, `PLAID_SECRET`, `PLAID_ENV` (`sandbox`|`production`),
  and `PLAID_TOKEN_ENC_KEY` to `internal/config` (same `getEnv` pattern), provisioned the
  same way as existing secrets (Terraform / Secret Manager).
- **Reuses:** `TransactionService.CreateTransaction` (auto-balance, atomic), account
  `metadata` JSONB, reconcile states, the numbered migration convention.

## 5. Data Model

### 5.1 New table `plaid_items` (migration `000007_plaid_items`)

Book-scoped record of one Plaid connection (Item).

| Column | Type | Notes |
|---|---|---|
| `guid` | UUID PK | |
| `book_guid` | UUID FK → books | multi-tenancy scope |
| `item_id` | TEXT | Plaid Item id |
| `institution_name` | TEXT | display (e.g. "RBC") |
| `access_token_ciphertext` | BYTEA | AES-256-GCM ciphertext |
| `access_token_nonce` | BYTEA | per-record nonce |
| `sync_cursor` | TEXT | `/transactions/sync` cursor (nullable) |
| `import_pending` | BOOLEAN | default `false`; user setting |
| `last_synced_at` | TIMESTAMPTZ | nullable |
| `version` | INT | OCC, default 1 |
| `created_at` / `updated_at` | TIMESTAMPTZ | |

### 5.2 Account ↔ Plaid-account link (1:1)

Stored on `accounts.metadata` JSONB:

```json
{ "plaid": { "item_guid": "<plaid_items.guid>", "account_id": "<plaid account_id>" } }
```

Invariant: a given Plaid `account_id` maps to **one** Antimoney account, and an Antimoney
account has **at most one** Plaid link. Enforced in the service on link creation.

### 5.2b Staging table `plaid_staged_transactions` (migration `000010`)

Every transaction fetched from `/transactions/sync` is staged durably **before** the
cursor is persisted — Plaid never re-sends data behind the cursor, so without staging a
dropped response or closed tab would lose those transactions permanently. Suggestions
are rebuilt from staging on every sync (dismissed suggestions reappear until imported);
rows are deleted on import; `removed` deltas delete staged rows, `modified` deltas
upsert them; a posted transaction whose pending predecessor was already imported is
skipped via `pending_transaction_id` correlation. The table is keyed on
`(book_guid, transaction_id)` and cascades on item disconnect. Staging is also the
**source of truth for import**: the client sends only `transaction_id` +
`category_account_guid`; date/description/amount/bank-account are read server-side.

### 5.3 Dedupe key

Each imported transaction stores its Plaid id on `transactions.metadata`:

```json
{ "plaid": { "transaction_id": "<plaid transaction_id>" } }
```

Sync checks this (per book) before creating, mirroring how GnuCash uses the OFX FITID.

### 5.4 Encryption at rest

`access_token` is encrypted with **AES-256-GCM**; the 32-byte key comes from
`PLAID_TOKEN_ENC_KEY` (base64). Store `nonce + ciphertext`; decrypt only in memory
immediately before a Plaid call. The plaintext token is never persisted and never logged.

## 6. Flows

### 6.1 Connect & map

1. Frontend → `POST /data/plaid/link-token` → backend `/link/token/create`
   (`products: ["transactions"]`, `country_codes: ["CA"]`) → `{ link_token }`.
2. Plaid Link opens; user authenticates with RBC; `onSuccess` → `public_token`.
3. Frontend → `POST /data/plaid/exchange { public_token }` → backend
   `/item/public_token/exchange` → `access_token` + `item_id`; encrypt + insert
   `plaid_items`; call `/accounts/get` → return `[{ account_id, name, mask, type }]`.
4. Frontend shows a mapping UI: for each Plaid account, pick an existing Antimoney
   account (1:1) or skip; toggle **Import pending transactions** (default off). Frontend
   → `POST /data/plaid/link { item_guid, mappings:[{account_id, account_guid}], import_pending }`
   → writes account metadata links + the `import_pending` setting.

### 6.2 Sync triggers & feedback

- **First-open-of-day:** account data returned to the register includes the link's
  `last_synced_at`. When `AccountRegister` mounts for a linked account and
  `last_synced_at` is before *today* (America/Toronto), the frontend triggers a sync.
- **Manual:** a **"Sync now"** button on the register and on DataManagement.
- **Feedback:** while syncing, show a status indicator ("Syncing <institution>…"); on
  success show "<N> transaction(s) ready to import" (the durable staged count, which
  includes still-unimported suggestions from earlier syncs) and open the matcher when
  `N > 0`; on failure show a generic message ("Couldn't sync <institution> — please try
  again."). The backend returns structured status and never leaks raw Plaid/internal
  errors.

### 6.3 Fetch → match → import

1. `POST /data/plaid/sync { item_guid }` → backend pages `/transactions/sync` (≤3 pages
   per call): each page's `added`/`modified` are **staged** and `removed` ids dropped
   from staging *before* the cursor is persisted; then `last_synced_at = now`. The
   response carries `has_more` when the page cap stopped mid-stream.
2. **Suggestions are rebuilt from staging** (durable — survive lost responses and
   dismissed modals): staged rows for mapped accounts, minus already-imported ones
   (SQL `NOT EXISTS` on the metadata dedupe key), minus `pending` ones when
   `import_pending` is false. A posted transaction whose pending predecessor was already
   imported is excluded via `pending_transaction_id`.
3. For each suggestion, `Categorizer.Suggest(book, txn)` proposes a counter account
   (normalized exact match first, then substring with LIKE metacharacters escaped);
   category names are resolved in one batched query.
4. The overlay lists rows: date, description, amount, the (fixed) linked bank account,
   an editable **category account** dropdown (pre-filled with the suggestion when
   present), an include/exclude toggle, and a **Dismiss** action (`POST /dismiss`)
   that permanently hides the suggestion (excluded rows merely reappear next sync;
   dismissed ones never do).
5. User fixes uncategorized rows and confirms → `POST /data/plaid/import` with
   **only** `{ transaction_id, category_account_guid }` per row.
6. Backend, per row, loads date/description/amount/bank-account **from staging** (a
   tampered client cannot inject financial values), dedupes, and calls
   `CreateTransaction`: split 1 = linked bank account, split 2 = chosen category
   account, `reconcile_state = 'c'`, `plaid.transaction_id` in metadata. The split value
   sign is derived from Plaid's amount convention (for depository accounts a
   **positive** `amount` means money *leaving* the account). Atomic per transaction;
   imported rows are removed from staging; the response reports
   `{ imported, failed[] }`. A partial unique index on the metadata dedupe key is the
   DB-level idempotency backstop against concurrent imports.

### 6.4 Disconnect

`DELETE /data/plaid/items/{guid}` → Plaid `/item/remove` → delete the row → clear the
`plaid` link from the affected accounts' metadata. Already-imported transactions are left
intact.

## 7. Categorization (pluggable)

```go
type Categorizer interface {
    // Suggest returns the counter (category) account for a transaction, if it can.
    Suggest(ctx context.Context, bookGUID string, txn PlaidTxn) (accountGUID string, ok bool)
}
```

- **MVP — `HistoryCategorizer`:** find the most recent prior transaction in the book whose
  payee/description matches (normalized exact match, then substring), and reuse that
  transaction's non-bank split account. This mimics GnuCash's description→account memory.
- **Future (no rework):** `BayesianCategorizer`, `PlaidTaxonomyCategorizer` (map Plaid's
  `personal_finance_category`), or a `RuleCategorizer` — swapped in or chained behind the
  same interface.

## 8. Security & Privacy

- `PLAID_SECRET` and `PLAID_TOKEN_ENC_KEY` live in server config only; provisioned like
  existing secrets; never sent to the frontend; never logged.
- `access_token` encrypted at rest (§5.4); decrypted only in memory for Plaid calls.
- `public_token` is the only client→server credential.
- All `/data/plaid` routes sit behind `RequireAuth`; every query is scoped to `book_guid`,
  so a user can only link/sync/import within their own book (IDOR-safe). Existing
  body-size and rate-limit middleware apply.

## 9. Error Handling

- **Re-auth (`ITEM_LOGIN_REQUIRED`)**: surface a "reconnect needed" status; MVP resolves it
  by re-running Connect (update-mode link token is a follow-up).
- **Plaid/network errors**: generic user-facing message; logged server-side without
  sensitive data.
- **Cloud Run 30s timeout**: sync is a single synchronous request; `/transactions/sync` is
  fast. If `has_more` pagination runs long, cap the pages per request and continue on the
  next sync (cursor persists progress).

## 10. Testing

- `PlaidClient` interface → `fakePlaidClient` with fixtures (link-token, exchange,
  sync pages) for fast unit tests; no network.
- **Unit:** AES-GCM encrypt/decrypt round-trip; dedupe by `transaction_id`;
  `HistoryCategorizer`; cursor advance/persist; 1:1 mapping-invariant enforcement;
  import → `CreateTransaction` (cleared + metadata written).
- **Integration:** handler tests with the fake client + a test DB, asserting book-scoping
  / IDOR protection.
- **Manual / e2e:** Plaid **Sandbox** (`user_good` / `pass_good`) against a sandbox
  institution for the full Link→import path; real **RBC** validated in production with the
  trial Item.

## 11. API Surface (new, under `/data/plaid`)

| Method & path | Purpose |
|---|---|
| `POST /link-token` | Create a Plaid Link token |
| `POST /exchange` | Exchange `public_token`; create Item; return bank accounts |
| `POST /link` | Persist 1:1 account mappings + `import_pending` |
| `POST /sync` | Run `/transactions/sync`; return deduped, categorized suggestions |
| `POST /import` | Create transactions for confirmed rows (cleared); financial data read from staging |
| `POST /dismiss` | Permanently hide staged suggestions (rows kept for dedupe/correlation) |
| `DELETE /items/{guid}` | Disconnect an Item and clear links (aborts if Plaid removal fails) |
| `GET /items` | List connected Items (non-sensitive fields) for the Connected-banks UI |

## 12. Follow-ups (post-MVP)

Webhooks; scheduled sync; applying `modified`/`removed` deltas and `pending→posted`
reconciliation; update-mode reconnect; Bayesian / Plaid-taxonomy / rules categorizers;
multi-currency display; encryption-key rotation.

## 13. Known limitations

- **Editing a Plaid-imported transaction unreconciles it.** Imports land as cleared
  (`'c'`), but `UpdateTransaction` deletes and re-inserts all splits with
  `reconcile_state = 'n'` (see CLAUDE.md), so any later edit silently un-clears the
  transaction. Documented in code (`PlaidService.Import`); a user-visible warning in
  the edit UI is a future enhancement.
- **Auto-sync day boundary is `America/Toronto`** (the target bank's locale), centralized
  in `frontend/src/utils/plaidSync.ts`. Users in other timezones may see the
  "first open of the day" boundary shift by a few hours until it is made configurable.
- **Amounts assume 2-decimal currencies.** Plaid float amounts are converted with a
  fixed denominator of 100 (cents) — correct for CAD/USD (the MVP scope), wrong for
  zero-decimal (JPY) or 3-decimal (BHD) currencies, which would need the account
  commodity's exponent (see ADR-001 in `docs/adr.md`).
- **Staging lifecycle.** Staged rows persist until imported, dismissed, or the item is
  disconnected (CASCADE). Rows for unmapped bank accounts stay invisible-but-staged and
  surface automatically once the account is mapped. A bank-side `removed` for an
  already-imported transaction is logged (book-vs-bank divergence) but never deletes the
  user's books; a posted transaction whose value diverges from its imported pending
  predecessor stays visible so the user can act on the correction (the value comparison
  is sign-aware over the linked bank account's split, so a reversal never collides with
  the original charge).
- **Dismissal is permanent by design.** A dismissed suggestion never resurfaces — not
  on bank-side `modified` deltas (even value changes), and not when a dismissed pending
  posts under a new `transaction_id` (the flag is inherited via
  `pending_transaction_id`; dismissed rows survive bank-side `removed` deltas as
  invisible tombstones precisely so the inheritance works even when the removal and
  the posted successor arrive in different sync pages or calls). Rationale: "never
  import this transaction" is an explicit user decision; silently resurrecting it on
  bank edits would make dismissal untrustworthy. Re-importing a dismissed transaction
  requires disconnect+reconnect.
- **Token crypto compatibility & rotation.** Ciphertexts are sealed with AAD =
  `(book_guid, item_id)`; tokens sealed by pre-AAD builds still decrypt via a legacy
  nil-AAD fallback and are re-sealed with the primary key + AAD on first use — the
  same opportunistic re-seal is what completes a key rotation without forcing users
  to re-link. A token no key can decrypt does not brick the item: disconnect proceeds
  with local cleanup (the Plaid-side Item must then be removed in the dashboard).
- **`plaid_migration_audit` retention.** Rows backed up by destructive migrations are
  kept indefinitely as an audit trail (they contain only ciphertexts, never plaintext
  tokens); operators may prune manually. The table is dropped by 000008's down.
- **E2E coverage.** Unit/integration suites cover backend and component logic; the
  real Plaid Link flow cannot run headless without sandbox credentials, so end-to-end
  validation of connect→sync→import stays a manual Sandbox checklist (§10).
- **Concurrent syncs are serialized per item** (Postgres advisory lock). The loser
  doesn't fetch: it serves the durable staged suggestions and marks the response
  `in_progress` so the UI can tell the user results may be incomplete.
