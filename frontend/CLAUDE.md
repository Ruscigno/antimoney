# Frontend CLAUDE.md

React + TypeScript SPA built with Vite. All API calls go through `src/api/client.ts`.

## Commands

```bash
npm run dev      # Dev server at http://localhost:5173 (proxies /api and /auth to :8000)
npm run build    # Type-check + Vite production build
npm run lint     # ESLint
npm run test     # Vitest unit tests with coverage
```

For e2e tests, run from the repo root: `make e2e`

## Key Files

- `src/api/client.ts` — all API calls; handles JWT injection and error normalization
- `src/types/index.ts` — TypeScript interfaces mirroring backend JSON contracts
- `src/i18n.ts` — all UI strings in English + pt-BR
- `src/auth/` — auth context; token lives in `localStorage` under key `antimoney-token`
- `src/components/Register.tsx` — the main register view with infinite scroll
- `src/components/TransactionForm.tsx` — create/edit/duplicate transaction modal

## Security & Architecture Best Practices

- **Token Storage**: JWTs currently live in `localStorage`, which is susceptible to XSS. In future iterations, consider migrating to `HttpOnly`, `SameSite=Strict` secure cookies for authentication tokens.
- **XSS (Cross-Site Scripting) Prevention**: React inherently protects against XSS, but strictly avoid using `dangerouslySetInnerHTML`. Always sanitize user-provided descriptions or metadata.
- **Content Security Policy (CSP)**: Set strict CSP headers in production deployments to block unsanctioned script execution and restrict resource origins.
- **Dependency Auditing**: Regularly run `npm audit` and keep third-party packages updated to patch known vulnerabilities.

## API Client Rules

- **All requests go through `fetchJSON<T>()`** in `client.ts`. Do not call `fetch` directly.
- **401 forces a hard reload**: token is cleared and `window.location.reload()` is called immediately. There is no graceful error recovery path for auth failures.
- **204 returns `undefined`**: DELETE endpoints return 204; `fetchJSON` returns `undefined as T`. Callers must type accordingly.
- **Errors are `{ error: string }` shaped**: Non-2xx responses are parsed as JSON and `body.error` is thrown as an `Error`. If JSON parsing fails, `res.statusText` is used.

## i18n Rules

- Every visible string must use `t('key')` — never hardcode user-facing text.
- **Both locales are required**: Adding a key to `en` without adding it to `pt-BR` (or vice versa) causes the missing locale to render the raw key string.
- The fallback chain is: requested locale → `en` → key name.
- `formatCurrency()` currently formats in BRL regardless of locale. It is not truly multi-currency.

## Pagination

Two patterns coexist — use the right one per endpoint:

| Endpoint | Pattern | Params |
|---|---|---|
| `/accounts/{id}/register` | Cursor-based | `cursor_date`, `direction` (`before`\|`after`\|`around`), `limit` |
| `/transactions` | Offset-based | `limit`, `offset` |

The register's `getAccountRegisterPaged()` must always supply `cursor_date`. Omitting it causes the backend to return all rows (no pagination), which is slow for large accounts.

**Infinite scroll in `Register.tsx`**: when prepending rows (loading "before"), the code manually adjusts `scrollTop` to preserve the user's visual position.

## Reconcile State

Splits have three states: `'n'` (unreconciled), `'c'` (cleared), `'y'` (reconciled).

- The Register UI cycles `n ↔ c` only. It never sets `'y'` directly.
- Only the batch reconcile wizard (`ReconcileWizard.tsx` → `batchReconcileSplits`) sets `'y'`.
- `toggleSplitAcknowledge` is misnamed — the toggle logic is in the frontend; the backend just sets whatever state it receives.

## Account Type Display

Credit-normal account types (Liability, Credit, Income, Equity) show **Increase / Decrease** column headers in the Register instead of Deposit / Withdrawal. This is purely a label change — the split `value_num` sign convention does not change.

## Testing

- Unit tests use **Vitest** (`vitest.config.ts`). Run with `npm run test`.
- E2e tests use **Playwright** (`playwright.config.ts` + `e2e/`). Run from repo root with `make e2e`.
- No mocking of the API client in unit tests — use MSW or test with real data where possible.
