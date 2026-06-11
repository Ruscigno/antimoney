import { test, expect } from '@playwright/test';

// ──────────────────────────────────────────────
// Antimoney — Full E2E Test Suite
// Requires: backend on :8000, frontend on :5173
// ──────────────────────────────────────────────

const BASE = 'http://localhost:5173';

test.describe.serial('Antimoney E2E', () => {
    let authToken = '';

    test.beforeAll(async ({ request }) => {
        const res = await request.post(`${BASE}/auth/register`, {
            data: { email: `e2e-${Date.now()}@example.com`, password: 'password', name: 'E2E' }
        });
        const data = await res.json();
        authToken = data.token;
    });

    test.beforeEach(async ({ page }) => {
        // Inject token map into localStorage on page init
        await page.addInitScript((token) => {
            Object.defineProperty(window, 'localStorage', {
                value: {
                    getItem: (k: string) => k === 'antimoney-token' ? token : null,
                    setItem: () => { },
                    removeItem: () => { },
                },
                writable: true
            });
        }, authToken);
    });

    // ── 1. Dashboard ────────────────────────────
    test('Dashboard loads with valid currency values (no NaN)', async ({ page }) => {
        await page.goto(BASE);
        await page.waitForSelector('.stats-grid');

        // All 5 metric cards should exist (Net Worth + Assets + Liabilities + Income + Expenses)
        const cards = page.locator('.stat-card');
        await expect(cards).toHaveCount(5);

        // No card should show NaN
        const cardTexts = await cards.allTextContents();
        for (const text of cardTexts) {
            expect(text).not.toContain('NaN');
        }

        // Net Worth card
        await expect(page.locator('.card-title', { hasText: 'Net Worth' })).toBeVisible();
    });

    // ── 2. i18n: English labels ─────────────────
    test('English labels are displayed by default', async ({ page }) => {
        await page.goto(BASE);
        await page.waitForSelector('.stats-grid');

        // Sidebar should have English labels
        await expect(page.locator('.nav-item', { hasText: 'Dashboard' })).toBeVisible();
        await expect(page.locator('.nav-item', { hasText: 'Chart of Accounts' })).toBeVisible();
        await expect(page.locator('.nav-item', { hasText: 'Transactions' })).toBeVisible();

        // Dashboard summary cards
        await expect(page.locator('.stat-card', { hasText: '🏦 Assets' })).toBeVisible();
        await expect(page.locator('.stat-card', { hasText: '💳 Liabilities' })).toBeVisible();
        await expect(page.locator('.stat-card', { hasText: '📈 Income' })).toBeVisible();
        await expect(page.locator('.stat-card', { hasText: '📉 Expenses' })).toBeVisible();
        await expect(page.locator('text=Financial overview')).toBeVisible();
    });

    // ── 3. Chart of Accounts ────────────────────
    test('Chart of Accounts shows account tree', async ({ page }) => {
        await page.goto(`${BASE}/accounts`);
        await page.waitForSelector('.account-tree');

        await expect(page.locator('text=Ativos').first()).toBeVisible();
        await expect(page.locator('text=Passivos').first()).toBeVisible();
        await expect(page.locator('text=Receitas').first()).toBeVisible();
        await expect(page.locator('text=Despesas').first()).toBeVisible();
        await expect(page.locator('text=Conta Corrente').first()).toBeVisible();
    });

    // ── 4. Account Register ─────────────────────
    test('Account Register opens for Conta Corrente', async ({ page }) => {
        await page.goto(`${BASE}/accounts`);
        await page.waitForSelector('.account-tree');

        await page.locator('.account-name', { hasText: 'Conta Corrente' }).click();
        await page.waitForURL(/\/accounts\/.+/);

        await expect(page.locator('.page-title')).toContainText('Conta Corrente');
        await expect(page.locator('#btn-new-tx')).toBeVisible();
    });

    // ── 5. Keyboard: ESC closes modal ───────────
    test('ESC key closes transaction modal', async ({ page }) => {
        await page.goto(`${BASE}/transactions`);
        await page.waitForSelector('.page-title');

        await page.locator('#btn-new-tx-global').click();
        await page.waitForSelector('.modal');
        await page.keyboard.press('Escape');
        await page.waitForSelector('.modal', { state: 'detached' });
    });

    // ── 6. Keyboard: N opens new transaction ────
    test('N key opens new transaction form', async ({ page }) => {
        await page.goto(`${BASE}/transactions`);
        await page.waitForSelector('.page-title');

        await page.keyboard.press('n');
        await page.waitForSelector('.modal');
        await expect(page.locator('.modal-title')).toContainText('New Transaction');

        // Close it
        await page.keyboard.press('Escape');
        await page.waitForSelector('.modal', { state: 'detached' });
    });

    // ── 7. Keyboard: Alt+1/2/3 navigation ──────
    test('Alt+1 navigates to Dashboard', async ({ page }) => {
        await page.goto(`${BASE}/transactions`);
        await page.waitForSelector('.page-title');
        await page.keyboard.press('Alt+1');
        await page.waitForURL(`${BASE}/`);
        await expect(page.locator('.page-title')).toContainText('Dashboard');
    });

    test('Alt+2 navigates to Accounts', async ({ page }) => {
        await page.goto(BASE);
        await page.waitForSelector('.page-title');
        await page.keyboard.press('Alt+2');
        await page.waitForURL(`${BASE}/accounts`);
        await expect(page.locator('.page-title')).toContainText('Chart of Accounts');
    });

    test('Alt+3 navigates to Transactions', async ({ page }) => {
        await page.goto(BASE);
        await page.waitForSelector('.page-title');
        await page.keyboard.press('Alt+3');
        await page.waitForURL(`${BASE}/transactions`);
        await expect(page.locator('.page-title')).toContainText('Transactions');
    });

    test('Alt+4 navigates to Data Management', async ({ page }) => {
        await page.goto(BASE);
        await page.waitForSelector('.page-title');
        await page.keyboard.press('Alt+4');
        await page.waitForURL(`${BASE}/data`);
        await expect(page.locator('.page-title')).toContainText('Data Management');
    });

    // ── 8. Transaction Form auto-fill + create ──
    test('Transaction Form auto-fills account and creates transaction', async ({ page }) => {
        // Navigate to Conta Corrente register
        await page.goto(`${BASE}/accounts`);
        await page.waitForSelector('.account-tree');
        await page.locator('.account-name', { hasText: 'Conta Corrente' }).click();
        await page.waitForURL(/\/accounts\/.+/);

        // Click New Transaction
        await page.locator('#btn-new-tx').click();
        await page.waitForSelector('.modal');

        // The first split should be pre-filled with Conta Corrente
        const firstSelectText = await page.locator('#split-account-0').textContent();
        expect(firstSelectText).toContain('Conta Corrente'); // Should be pre-filled

        // Description should be auto-focused
        const descriptionInput = page.locator('#tx-description');
        await expect(descriptionInput).toBeFocused();

        // Fill the form
        await descriptionInput.fill('Monthly Salary');
        await page.locator('#split-value-0').fill('5000');

        // Select the Salário income account for the 2nd split
        await page.locator('#split-account-1').click();
        await page.locator('#split-account-1').fill('Sal');
        const salarioOption = page.locator('.account-picker-option', { hasText: 'Sal' }).first();
        await salarioOption.waitFor({ state: 'visible' });
        await salarioOption.click();
        await page.locator('#split-value-1').fill('-5000');

        // Should show balanced
        await expect(page.locator('text=Balanced')).toBeVisible();

        // Submit
        await page.locator('#tx-submit').click();
        await page.waitForSelector('.modal', { state: 'detached' });

        // The register should now show the transaction with a deposit
        await page.waitForSelector('.register-table');
        await expect(page.locator('text=Monthly Salary')).toBeVisible();
    });

    // ── 9. Register shows # column ──────────────
    test('Register has # column with sequential numbers', async ({ page }) => {
        await page.goto(`${BASE}/accounts`);
        await page.waitForSelector('.account-tree');
        await page.locator('.account-name', { hasText: 'Conta Corrente' }).click();
        await page.waitForURL(/\/accounts\/.+/);
        await page.waitForSelector('.register-table');

        // Check # header
        await expect(page.locator('.register-table th.col-num')).toHaveText('#');

        // First row should have number 1
        const firstNum = page.locator('.register-table td.col-num').first();
        await expect(firstNum).toHaveText('1');
    });

    // ── 10. Transactions page ───────────────────
    test('Transactions page lists entries with # column', async ({ page }) => {
        await page.goto(`${BASE}/transactions`);
        await page.waitForSelector('.register-table');

        const rows = page.locator('.register-table tbody tr');
        const count = await rows.count();
        expect(count).toBeGreaterThan(0);

        // # header
        await expect(page.locator('.register-table th.col-num')).toHaveText('#');
    });

    // ── 11. Dashboard reflects balances ─────────
    test('Dashboard reflects transaction in balances', async ({ page }) => {
        await page.goto(BASE);
        await page.waitForSelector('.stats-grid');

        const assetsCard = page.locator('.stat-card', { hasText: '🏦 Assets' });
        const assetsText = await assetsCard.textContent();
        expect(assetsText).not.toContain('NaN');
        // After creating a 5000 salary transaction, assets should be non-zero
        expect(assetsText).toContain('5');
    });

    // ── 12. Delete transaction ──────────────────
    test('Delete transaction removes it from list', async ({ page }) => {
        await page.goto(`${BASE}/transactions`);
        await page.waitForSelector('.register-table');

        const initialRows = await page.locator('.register-table tbody tr').count();
        expect(initialRows).toBeGreaterThan(0);

        page.on('dialog', dialog => dialog.accept());
        await page.locator('.btn-danger').first().click();

        // Wait for row to be removed or empty state
        await page.waitForFunction(
            (initial) => {
                const rows = document.querySelectorAll('.register-table tbody tr');
                const empty = document.querySelector('.empty-state');
                return rows.length < initial || empty !== null;
            },
            initialRows,
            { timeout: 10000 }
        );
    });

    // ── 13. Data Management ─────────────────────
    test('Data Management page elements are visible', async ({ page }) => {
        await page.goto(`${BASE}/data`);
        await page.waitForSelector('.page-title');

        await expect(page.locator('.page-title')).toHaveText('Data Management');
        // Check for Export and Import buttons
        await expect(page.locator('button', { hasText: 'Export to JSON' })).toBeVisible();
        await expect(page.locator('button', { hasText: 'Import from JSON' })).toBeVisible();
    });

    // ── 14. Import from JSON ─────────────────────
    test('Import from JSON replaces existing data', async ({ page }) => {
        await page.goto(`${BASE}/data`);
        await page.waitForSelector('.page-title');

        // 1. Export current data to get valid GUIDs for this user
        const downloadPromise = page.waitForEvent('download');
        await page.locator('button', { hasText: 'Export to JSON' }).click();
        const download = await downloadPromise;
        const exportPath = 'test-results/export.json';
        await download.saveAs(exportPath);

        // 2. Import it back
        const fileChooserPromise = page.waitForEvent('filechooser');
        await page.locator('button', { hasText: 'Import from JSON' }).click();
        const fileChooser = await fileChooserPromise;
        await fileChooser.setFiles(exportPath);

        // Wait for success message
        await page.waitForSelector('text=import successful', { timeout: 10000 });

        // Verify data: check Chart of Accounts
        await page.goto(`${BASE}/accounts`);
        await page.waitForSelector('.account-tree');

        // Verify that seeded accounts are still there (re-imported)
        await expect(page.locator('text=Ativos').first()).toBeVisible();
        await expect(page.locator('text=Passivos').first()).toBeVisible();
    });
});

// ──────────────────────────────────────────────
// Register search — self-contained: registers its own user, seeds two
// transactions via the API, and authenticates via the auth_token cookie.
// Independent of the suite above so it stands on its own.
// ──────────────────────────────────────────────
interface ApiAccount {
    guid: string;
    name: string;
    account_type: string;
    placeholder: boolean;
}

test.describe.serial('Register search E2E', () => {
    let token = '';
    let checkingGuid = '';

    const balancedSplits = (debit: string, credit: string, cents: number) => [
        { account_guid: debit, memo: '', value_num: cents, value_denom: 100, quantity_num: cents, quantity_denom: 100 },
        { account_guid: credit, memo: '', value_num: -cents, value_denom: 100, quantity_num: -cents, quantity_denom: 100 },
    ];

    test.beforeAll(async ({ request }) => {
        const reg = await request.post(`${BASE}/auth/register`, {
            data: { email: `search-${Date.now()}@example.com`, password: 'Password1', name: 'Search' },
        });
        token = (await reg.json()).token;
        const headers = { Cookie: `auth_token=${token}` };

        const accountsRes = await request.get(`${BASE}/api/accounts`, { headers });
        const accounts: ApiAccount[] = await accountsRes.json();
        const checking = accounts.find(a => a.name === 'Conta Corrente');
        if (!checking) throw new Error('Seed account "Conta Corrente" not found — check seed data');
        const income = accounts.find(a => a.account_type === 'INCOME' && !a.placeholder);
        if (!income) throw new Error('No non-placeholder INCOME account found in seed data');
        checkingGuid = checking.guid;

        // Two transactions with distinct descriptions and amounts so search can narrow.
        await request.post(`${BASE}/api/transactions`, {
            headers,
            data: { custom_id: '', post_date: '2025-10-01T11:00:00Z', description: 'Acme Salary Payment', splits: balancedSplits(checking.guid, income.guid, 500000) },
        });
        await request.post(`${BASE}/api/transactions`, {
            headers,
            data: { custom_id: '', post_date: '2025-10-02T11:00:00Z', description: 'Tangerine Grocery Run', splits: balancedSplits(checking.guid, income.guid, 25000) },
        });
    });

    test.beforeEach(async ({ page }) => {
        // The app reads the JWT from the HttpOnly auth_token cookie.
        await page.context().addCookies([{ name: 'auth_token', value: token, url: BASE }]);
    });

    test('filters the register by partial text, shows a count, and clears', async ({ page }) => {
        await page.goto(`${BASE}/accounts/${checkingGuid}`);
        await page.waitForSelector('.register-table');

        const searchInput = page.locator('.register-search-input');
        await expect(searchInput).toBeVisible();

        // Partial, case-insensitive match narrows to the matching row only.
        await searchInput.fill('acme');
        await expect(page.locator('.register-table td.col-description', { hasText: 'Acme Salary Payment' })).toBeVisible();
        await expect(page.locator('.register-table td.col-description', { hasText: 'Tangerine Grocery Run' })).toHaveCount(0);
        await expect(page.locator('.register-search-count')).toBeVisible();

        // A non-matching query shows the "no match" state and hides the table.
        await searchInput.fill('zzz-nonexistent-zzz');
        await expect(page.locator('.register-search-count')).toContainText('No transactions match');
        await expect(page.locator('.register-table')).toHaveCount(0);

        // Clearing via the × button restores the full register.
        await page.locator('.register-search-clear').click();
        await expect(searchInput).toHaveValue('');
        await expect(page.locator('.register-table td.col-description', { hasText: 'Tangerine Grocery Run' })).toBeVisible();
    });

    test('matches case-insensitively and by amount', async ({ page }) => {
        await page.goto(`${BASE}/accounts/${checkingGuid}`);
        await page.waitForSelector('.register-table');
        const searchInput = page.locator('.register-search-input');

        // Uppercase query matches lowercase content.
        await searchInput.fill('TANGERINE');
        await expect(page.locator('.register-table td.col-description', { hasText: 'Tangerine Grocery Run' })).toBeVisible();
        await expect(page.locator('.register-table td.col-description', { hasText: 'Acme Salary Payment' })).toHaveCount(0);

        // Typing the amount finds the salary (5000.00).
        await searchInput.fill('5000');
        await expect(page.locator('.register-table td.col-description', { hasText: 'Acme Salary Payment' })).toBeVisible();
    });

    // ── Plaid (smoke) ───────────────────────────
    // The full Link flow needs sandbox credentials and cannot run headless
    // (spec §10 keeps it a manual Sandbox checklist). This smoke test pins the
    // wiring: the section renders, and when the backend has no PLAID_* env
    // (routes unmounted) the UI degrades to a friendly error — never a crash
    // or a raw backend message.
    test('Plaid Connect Bank section renders and degrades gracefully', async ({ page }) => {
        await page.goto(`${BASE}/data`);
        const connectBtn = page.getByRole('button', { name: /Connect bank|Conectar banco/i });
        await expect(connectBtn).toBeVisible();
        await connectBtn.click();
        // Either Plaid is configured (Link opens an iframe) or it is not
        // (friendly error message) — both are acceptable; a crash is not.
        await expect(
            page.locator('.message.error').or(page.locator('iframe[id^="plaid-link"]')),
        ).toBeVisible({ timeout: 10000 });
    });
});
