package seed

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedDatabase populates the database with default currencies and a Chart of Accounts.
// It's idempotent — skips if data already exists.
func SeedDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	// Check if already seeded
	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM commodities").Scan(&count)
	if count > 0 {
		log.Println("Database already seeded, skipping")
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Create currencies
	currencies := []struct {
		guid, mnemonic, fullname string
		fraction                 int
	}{
		{"c0000000-0000-0000-0000-000000000b01", "BRL", "Brazilian Real", 100},
		{"c0000000-0000-0000-0000-000000000002", "USD", "US Dollar", 100},
		{"c0000000-0000-0000-0000-000000000e03", "EUR", "Euro", 100},
	}

	for _, c := range currencies {
		_, err := tx.Exec(ctx,
			`INSERT INTO commodities (guid, namespace, mnemonic, fullname, fraction)
			 VALUES ($1, 'CURRENCY', $2, $3, $4)`,
			c.guid, c.mnemonic, c.fullname, c.fraction,
		)
		if err != nil {
			return fmt.Errorf("insert currency %s: %w", c.mnemonic, err)
		}
	}

	brlGUID := "c0000000-0000-0000-0000-000000000b01"

	// 2. Create default Chart of Accounts (BRL-based)
	// Root account
	rootGUID := "a0000000-0000-0000-0000-000000000000"
	_, err = tx.Exec(ctx,
		`INSERT INTO accounts (guid, name, account_type, commodity_guid, parent_guid, placeholder, description)
		 VALUES ($1, 'Root Account', 'ROOT', $2, NULL, TRUE, 'Root of the account tree')`,
		rootGUID, brlGUID,
	)
	if err != nil {
		return fmt.Errorf("insert root account: %w", err)
	}

	// Top-level accounts
	type acct struct {
		guid, name, atype, description string
		placeholder                    bool
	}
	topLevel := []acct{
		{"a0000000-0000-0000-0000-000000000001", "Ativos", "ASSET", "Assets", true},
		{"a0000000-0000-0000-0000-000000000002", "Passivos", "LIABILITY", "Liabilities", true},
		{"a0000000-0000-0000-0000-000000000003", "Receitas", "INCOME", "Income", true},
		{"a0000000-0000-0000-0000-000000000004", "Despesas", "EXPENSE", "Expenses", true},
		{"a0000000-0000-0000-0000-000000000005", "Patrimônio", "EQUITY", "Equity", true},
	}

	for _, a := range topLevel {
		_, err := tx.Exec(ctx,
			`INSERT INTO accounts (guid, name, account_type, commodity_guid, parent_guid, placeholder, description)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			a.guid, a.name, a.atype, brlGUID, rootGUID, a.placeholder, a.description,
		)
		if err != nil {
			return fmt.Errorf("insert top-level account %s: %w", a.name, err)
		}
	}

	// Sub-accounts under Assets
	assetSubs := []acct{
		{"a0000000-0000-0000-0000-000000000011", "Conta Corrente", "BANK", "Checking account", false},
		{"a0000000-0000-0000-0000-000000000012", "Poupança", "BANK", "Savings account", false},
		{"a0000000-0000-0000-0000-000000000013", "Carteira", "CASH", "Wallet / cash on hand", false},
	}
	for _, a := range assetSubs {
		_, err := tx.Exec(ctx,
			`INSERT INTO accounts (guid, name, account_type, commodity_guid, parent_guid, placeholder, description)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			a.guid, a.name, a.atype, brlGUID, "a0000000-0000-0000-0000-000000000001", a.placeholder, a.description,
		)
		if err != nil {
			return fmt.Errorf("insert asset sub-account %s: %w", a.name, err)
		}
	}

	// Sub-accounts under Liabilities
	_, err = tx.Exec(ctx,
		`INSERT INTO accounts (guid, name, account_type, commodity_guid, parent_guid, placeholder, description)
		 VALUES ($1, 'Cartão de Crédito', 'CREDIT', $2, $3, FALSE, 'Credit card')`,
		"a0000000-0000-0000-0000-000000000021", brlGUID, "a0000000-0000-0000-0000-000000000002",
	)
	if err != nil {
		return fmt.Errorf("insert credit card account: %w", err)
	}

	// Sub-accounts under Expenses
	expenseSubs := []acct{
		{"a0000000-0000-0000-0000-000000000041", "Alimentação", "EXPENSE", "Food & groceries", false},
		{"a0000000-0000-0000-0000-000000000042", "Transporte", "EXPENSE", "Transportation", false},
		{"a0000000-0000-0000-0000-000000000043", "Moradia", "EXPENSE", "Housing", false},
		{"a0000000-0000-0000-0000-000000000044", "Lazer", "EXPENSE", "Entertainment", false},
		{"a0000000-0000-0000-0000-000000000045", "Saúde", "EXPENSE", "Healthcare", false},
		{"a0000000-0000-0000-0000-000000000046", "Educação", "EXPENSE", "Education", false},
	}
	for _, a := range expenseSubs {
		_, err := tx.Exec(ctx,
			`INSERT INTO accounts (guid, name, account_type, commodity_guid, parent_guid, placeholder, description)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			a.guid, a.name, a.atype, brlGUID, "a0000000-0000-0000-0000-000000000004", a.placeholder, a.description,
		)
		if err != nil {
			return fmt.Errorf("insert expense sub-account %s: %w", a.name, err)
		}
	}

	// Sub-accounts under Income
	incomeSubs := []acct{
		{"a0000000-0000-0000-0000-000000000031", "Salário", "INCOME", "Salary", false},
		{"a0000000-0000-0000-0000-000000000032", "Freelance", "INCOME", "Freelance income", false},
	}
	for _, a := range incomeSubs {
		_, err := tx.Exec(ctx,
			`INSERT INTO accounts (guid, name, account_type, commodity_guid, parent_guid, placeholder, description)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			a.guid, a.name, a.atype, brlGUID, "a0000000-0000-0000-0000-000000000003", a.placeholder, a.description,
		)
		if err != nil {
			return fmt.Errorf("insert income sub-account %s: %w", a.name, err)
		}
	}

	// Equity: Opening Balances
	_, err = tx.Exec(ctx,
		`INSERT INTO accounts (guid, name, account_type, commodity_guid, parent_guid, placeholder, description)
		 VALUES ($1, 'Saldos Iniciais', 'EQUITY', $2, $3, FALSE, 'Opening balances')`,
		"a0000000-0000-0000-0000-000000000051", brlGUID, "a0000000-0000-0000-0000-000000000005",
	)
	if err != nil {
		return fmt.Errorf("insert equity sub-account: %w", err)
	}

	// 3. Create book referencing root account
	_, err = tx.Exec(ctx,
		`INSERT INTO books (guid, root_account_guid) VALUES ($1, $2)`,
		"b0000000-0000-0000-0000-000000000001", rootGUID,
	)
	if err != nil {
		return fmt.Errorf("insert book: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed: %w", err)
	}

	log.Println("Database seeded successfully with default currencies and Chart of Accounts")
	return nil
}

// SeedUserBook creates a personal chart of accounts for a newly registered user's book.
// Uses the already-seeded BRL commodity. Generates fresh UUIDs for all accounts.
func SeedUserBook(ctx context.Context, pool *pgxpool.Pool, bookGUID string) error {
	brlGUID := "c0000000-0000-0000-0000-000000000b01"

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed user book: %w", err)
	}
	defer tx.Rollback(ctx)

	// Helper: insert account with generated GUID and return it
	insertAccount := func(name, atype, parentGUID string, placeholder bool, description string) (string, error) {
		var guid string
		var parentPtr *string
		if parentGUID != "" {
			parentPtr = &parentGUID
		}
		err := tx.QueryRow(ctx,
			`INSERT INTO accounts (name, account_type, commodity_guid, parent_guid, book_guid, placeholder, description)
			 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING guid`,
			name, atype, brlGUID, parentPtr, bookGUID, placeholder, description,
		).Scan(&guid)
		return guid, err
	}

	// Root account
	rootGUID, err := insertAccount("Root Account", "ROOT", "", true, "Root of the account tree")
	if err != nil {
		return fmt.Errorf("insert root: %w", err)
	}

	// Update book with root account
	_, err = tx.Exec(ctx, "UPDATE books SET root_account_guid = $1 WHERE guid = $2", rootGUID, bookGUID)
	if err != nil {
		return fmt.Errorf("update book root: %w", err)
	}

	// Top-level
	type acctDef struct {
		name, atype, description string
	}
	topDefs := []acctDef{
		{"Ativos", "ASSET", "Assets"},
		{"Passivos", "LIABILITY", "Liabilities"},
		{"Receitas", "INCOME", "Income"},
		{"Despesas", "EXPENSE", "Expenses"},
		{"Patrimônio", "EQUITY", "Equity"},
	}
	topGUIDs := make([]string, len(topDefs))
	for i, d := range topDefs {
		topGUIDs[i], err = insertAccount(d.name, d.atype, rootGUID, true, d.description)
		if err != nil {
			return fmt.Errorf("insert %s: %w", d.name, err)
		}
	}

	// Sub-accounts under Assets
	for _, sub := range []acctDef{
		{"Conta Corrente", "BANK", "Checking account"},
		{"Poupança", "BANK", "Savings account"},
		{"Carteira", "CASH", "Wallet / cash on hand"},
	} {
		if _, err := insertAccount(sub.name, sub.atype, topGUIDs[0], false, sub.description); err != nil {
			return fmt.Errorf("insert %s: %w", sub.name, err)
		}
	}

	// Sub-accounts under Liabilities
	if _, err := insertAccount("Cartão de Crédito", "CREDIT", topGUIDs[1], false, "Credit card"); err != nil {
		return fmt.Errorf("insert credit card: %w", err)
	}

	// Sub-accounts under Income
	for _, sub := range []acctDef{
		{"Salário", "INCOME", "Salary"},
		{"Freelance", "INCOME", "Freelance income"},
	} {
		if _, err := insertAccount(sub.name, sub.atype, topGUIDs[2], false, sub.description); err != nil {
			return fmt.Errorf("insert %s: %w", sub.name, err)
		}
	}

	// Sub-accounts under Expenses
	for _, sub := range []acctDef{
		{"Alimentação", "EXPENSE", "Food & groceries"},
		{"Transporte", "EXPENSE", "Transportation"},
		{"Moradia", "EXPENSE", "Housing"},
		{"Lazer", "EXPENSE", "Entertainment"},
		{"Saúde", "EXPENSE", "Healthcare"},
		{"Educação", "EXPENSE", "Education"},
	} {
		if _, err := insertAccount(sub.name, sub.atype, topGUIDs[3], false, sub.description); err != nil {
			return fmt.Errorf("insert %s: %w", sub.name, err)
		}
	}

	// Equity sub
	if _, err := insertAccount("Saldos Iniciais", "EQUITY", topGUIDs[4], false, "Opening balances"); err != nil {
		return fmt.Errorf("insert opening balances: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed user book: %w", err)
	}

	log.Printf("Seeded chart of accounts for book %s", bookGUID)
	return nil
}
