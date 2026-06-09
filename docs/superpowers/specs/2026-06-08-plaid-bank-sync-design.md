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
- Applying Plaid `modified`/`removed` sync deltas and reconciling `pending → posted`
  transitions beyond not creating duplicates.
- Update-mode/"reconnect" niceties beyond a basic disconnect + reconnect.

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
  success show "<N> new transactions" and open the matcher when `N > 0`; on failure show
  a generic message ("Couldn't sync <institution> — please try again."). The backend
  returns structured status and never leaks raw Plaid/internal errors.

### 6.3 Fetch → match → import

1. `POST /data/plaid/sync { item_guid }` → backend `/transactions/sync` using the stored
   cursor; advance and persist the cursor; set `last_synced_at = now`.
2. **Filter:** keep `added` transactions for mapped accounts; if `import_pending` is false,
   drop `pending` ones; drop any whose `transaction_id` already exists (dedupe).
   (`modified`/`removed` deltas advance the cursor but are not applied in MVP — documented
   follow-up.)
3. For each remaining transaction, call `Categorizer.Suggest(book, txn)` → a suggested
   counter account or none.
4. Return suggestions to the frontend. The overlay lists rows: date, description, amount,
   the (fixed) linked bank account, an editable **category account** dropdown
   (pre-filled with the suggestion when present), and an include/exclude toggle.
5. User fixes uncategorized rows and confirms → `POST /data/plaid/import` with the chosen
   `category_account_guid` per row.
6. Backend, per row, calls `CreateTransaction`: split 1 = linked bank account, split 2 =
   chosen category account, `reconcile_state = 'c'`, and stores `plaid.transaction_id` in
   metadata. The split value sign is derived from Plaid's amount convention (for
   depository accounts a **positive** `amount` means money *leaving* the account); the
   importer maps that to the bank-account split sign and negates it for the counter split.
   Atomic per transaction. Returns the imported count.

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
| `POST /import` | Create transactions for confirmed rows (cleared) |
| `DELETE /items/{guid}` | Disconnect an Item and clear links |

## 12. Follow-ups (post-MVP)

Webhooks; scheduled sync; applying `modified`/`removed` deltas and `pending→posted`
reconciliation; update-mode reconnect; Bayesian / Plaid-taxonomy / rules categorizers;
multi-currency display; encryption-key rotation.
