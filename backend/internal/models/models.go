package models

import (
	"encoding/json"
	"time"
)

// AccountType represents the type of account in the chart of accounts.
type AccountType string

const (
	AccountTypeRoot       AccountType = "ROOT"
	AccountTypeAsset      AccountType = "ASSET"
	AccountTypeBank       AccountType = "BANK"
	AccountTypeCash       AccountType = "CASH"
	AccountTypeLiability  AccountType = "LIABILITY"
	AccountTypeCredit     AccountType = "CREDIT"
	AccountTypeIncome     AccountType = "INCOME"
	AccountTypeExpense    AccountType = "EXPENSE"
	AccountTypeEquity     AccountType = "EQUITY"
	AccountTypeReceivable AccountType = "RECEIVABLE"
	AccountTypePayable    AccountType = "PAYABLE"
)

// IsDebitNormal returns true for account types where a debit (positive) increases the balance.
func (at AccountType) IsDebitNormal() bool {
	switch at {
	case AccountTypeAsset, AccountTypeBank, AccountTypeCash, AccountTypeExpense, AccountTypeReceivable:
		return true
	default:
		return false
	}
}

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"` // never expose
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Account struct {
	GUID        string      `json:"guid"`
	Name        string      `json:"name"`
	AccountType AccountType `json:"account_type"`

	ParentGUID  *string         `json:"parent_guid"`
	BookGUID    *string         `json:"book_guid,omitempty"`
	Placeholder bool            `json:"placeholder"`
	Description string          `json:"description"`
	Metadata    json.RawMessage `json:"metadata"`
	Version     int             `json:"version"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`

	// Computed fields (not stored directly)
	Children          []*Account `json:"children,omitempty"`
	Balance           float64    `json:"balance,omitempty"`
	ReconciledBalance float64    `json:"reconciled_balance"`
	LastReconciled    *time.Time `json:"last_reconciled,omitempty"`
}

type Book struct {
	GUID            string    `json:"guid"`
	RootAccountGUID *string   `json:"root_account_guid"`
	UserID          *string   `json:"user_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Transaction struct {
	GUID     string `json:"guid"`
	CustomID string `json:"custom_id"`

	BookGUID    *string         `json:"book_guid,omitempty"`
	PostDate    time.Time       `json:"post_date"`
	EnterDate   time.Time       `json:"enter_date"`
	Description string          `json:"description"`
	Metadata    json.RawMessage `json:"metadata"`
	Version     int             `json:"version"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`

	// Aggregate: always loaded with the transaction
	Splits []Split `json:"splits"`
}

type Split struct {
	GUID           string `json:"guid"`
	TxGUID         string `json:"tx_guid"`
	AccountGUID    string `json:"account_guid"`
	Memo           string `json:"memo"`
	ValueNum       int64  `json:"value_num"`
	ValueDenom     int64  `json:"value_denom"`
	QuantityNum    int64  `json:"quantity_num"`
	QuantityDenom  int64  `json:"quantity_denom"`
	ReconcileState string `json:"reconcile_state"`

	// Computed fields for frontend
	AccountName string `json:"account_name,omitempty"`
}

// RegisterEntry represents a single row in the account register view.
type RegisterEntry struct {
	TransactionGUID     string    `json:"transaction_guid"`
	CustomID            string    `json:"custom_id"`
	PostDate            time.Time `json:"post_date"`
	Description         string    `json:"description"`
	TransferAccount     string    `json:"transfer_account"`
	TransferAccountGUID string    `json:"transfer_account_guid"`
	Deposit             *float64  `json:"deposit,omitempty"`
	Withdrawal          *float64  `json:"withdrawal,omitempty"`
	Balance             float64   `json:"balance"`
	SplitMemo           string    `json:"split_memo"`
	SplitGUID           string    `json:"split_guid"`
	ReconcileState      string    `json:"reconcile_state"`
}
