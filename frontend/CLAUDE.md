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

## Responsive Design Rules

The app targets three breakpoints: desktop (≥ 769px), tablet/large phone (≤ 768px), and small phone (≤ 640px). Styles live in `src/index.css`.

**Core principle: every piece of information and every action must remain accessible on all screen sizes.** Hiding a column on mobile is only allowed if its essential content is surfaced another way in the same row (e.g., as a compact value or merged into another cell). Never hide the primary numeric value of a transaction or the main action buttons — adapt their presentation instead.

Rules to follow when adding or changing UI:

1. **Tables — column hiding**: Use CSS classes on `<td>` / `<th>` to control visibility. Existing classes: `col-num`, `col-memo`, `col-transfer` (hidden ≤ 768px); `col-tx-splits` (shows `.splits-detail` on desktop, `.splits-amount` on mobile). When hiding a column, ensure the suppressed info appears in a sibling cell via a mobile-only element.

2. **Action buttons**: Always provide a working action on mobile. Use `.tx-label-long` / `.tx-label-short` spans to show full text on desktop and a short label or symbol on narrow screens. Never hide an action button entirely — resize or abbreviate it instead.

3. **Grids**: Use CSS classes (`dashboard-stats-grid`, `dashboard-two-col`, `reconcile-funds-grid`) instead of inline `gridTemplateColumns` so media queries can reflow them. Inline grid styles cannot be overridden by media queries.

4. **Modals**: At ≤ 640px, modals go full-screen (see `.modal` rules). Make sure modal content is scrollable and no important controls fall below the fold on small screens.

5. **Touch targets**: All interactive elements must be at least 40px tall on mobile. The `.btn` rule already enforces `min-height: 40px` at ≤ 768px.

6. **Loading states**: Loading spinners use `.loading` which is `display: flex; justify-content: center; width: 100%`. Always use the `.loading` class (not raw inline styles) so it centers correctly inside any page container.

7. **Desktop parity check**: After any layout change, verify that desktop (≥ 769px) is visually unchanged. Media queries only apply at the listed breakpoints; base styles are desktop-first.

## Security & Architecture Best Practices

- **Token Storage**: JWTs are stored in an HttpOnly `SameSite=Strict` cookie (set by the backend). User metadata (not the token) is cached in `localStorage` under `antimoney-user` for fast initial render.
- **XSS (Cross-Site Scripting) Prevention**: React inherently protects against XSS, but strictly avoid using `dangerouslySetInnerHTML`. Always sanitize user-provided descriptions or metadata.
- **Content Security Policy (CSP)**: Set strict CSP headers in production deployments to block unsanctioned script execution and restrict resource origins.
- **Dependency Auditing**: Regularly run `npm audit` and keep third-party packages updated to patch known vulnerabilities.

## API Client Rules

- **All requests go through `fetchJSON<T>()`** in `client.ts`. Do not call `fetch` directly.
- **401 dispatches `auth:session-expired`**: the cached user is cleared from `localStorage` and a `window.dispatchEvent(new Event('auth:session-expired'))` is fired. `AuthContext` listens for this event and sets `user = null`, showing the login page without a hard reload.
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
