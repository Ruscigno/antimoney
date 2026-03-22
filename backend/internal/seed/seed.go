package seed

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedDatabase populates the database with a default Chart of Accounts for legacy purposes.
func SeedDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	// Let's assume SeedDatabase creates a master book if needed.
	// We will skip master book seeding for now and rely entirely on SeedUserBook.
	return nil
}

// SeedUserBook creates a personal chart of accounts for a newly registered user's book.
// Generates fresh UUIDs for all accounts.
func SeedUserBook(ctx context.Context, pool *pgxpool.Pool, bookGUID string) error {
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
			`INSERT INTO accounts (name, account_type, parent_guid, book_guid, placeholder, description)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING guid`,
			name, atype, parentPtr, bookGUID, placeholder, description,
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
