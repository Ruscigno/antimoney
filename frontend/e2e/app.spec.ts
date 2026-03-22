import { test, expect } from '@playwright/test';

// ──────────────────────────────────────────────
// Antimoney — Full E2E Test Suite
// Requires: backend on :8002, frontend on :5174
// ──────────────────────────────────────────────

const BASE = 'http://localhost:5174';

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

        await expect(page.locator('text=Ativos')).toBeVisible();
        await expect(page.locator('text=Passivos')).toBeVisible();
        await expect(page.locator('text=Receitas')).toBeVisible();
        await expect(page.locator('text=Despesas')).toBeVisible();
        await expect(page.locator('text=Conta Corrente')).toBeVisible();
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
});
