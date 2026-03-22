package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/antimoney/internal/auth"
)

type ImportExportHandler struct {
	pool *pgxpool.Pool
}

func NewImportExportHandler(pool *pgxpool.Pool) *ImportExportHandler {
	return &ImportExportHandler{pool: pool}
}

func (h *ImportExportHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/import", h.handleImport)
	r.Get("/export", h.handleExport)
	return r
}

type ExportData struct {
	Accounts     []ExportAccount     `json:"accounts"`
	Transactions []ExportTransaction `json:"transactions"`
}

type ExportAccount struct {
	GUID        string  `json:"guid"`
	Name        string  `json:"name"`
	AccountType string  `json:"account_type"`
	ParentGUID  *string `json:"parent_guid"`
	Placeholder bool    `json:"placeholder"`
	Description string  `json:"description"`
}

type ExportTransaction struct {
	GUID        string        `json:"guid"`
	PostDate    string        `json:"post_date"`
	EnterDate   string        `json:"enter_date"`
	Description string        `json:"description"`
	Splits      []ExportSplit `json:"splits"`
}

type ExportSplit struct {
	GUID           string `json:"guid"`
	AccountGUID    string `json:"account_guid"`
	Memo           string `json:"memo"`
	ValueNum       int64  `json:"value_num"`
	ValueDenom     int64  `json:"value_denom"`
	QuantityNum    int64  `json:"quantity_num"`
	QuantityDenom  int64  `json:"quantity_denom"`
	ReconcileState string `json:"reconcile_state"`
}

func (h *ImportExportHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	var data ExportData
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json format")
		return
	}

	bookGUID := auth.BookGUIDFromCtx(r.Context())
	if bookGUID == "" {
		writeError(w, http.StatusUnauthorized, "missing book guid")
		return
	}

	// Ensure a ROOT account exists. If not, create one and wrap top-level accounts inside it.
	hasRoot := false
	for _, acc := range data.Accounts {
		if acc.AccountType == "ROOT" {
			hasRoot = true
			break
		}
	}

	if !hasRoot {
		rootGuid := uuid.New().String()
		for i, acc := range data.Accounts {
			if acc.ParentGUID == nil {
				g := rootGuid
				data.Accounts[i].ParentGUID = &g
			}
		}
		rootAcc := ExportAccount{
			GUID:        rootGuid,
			Name:        "Root Account",
			AccountType: "ROOT",
			ParentGUID:  nil,
			Placeholder: true,
			Description: "Root of the account tree",
		}
		data.Accounts = append([]ExportAccount{rootAcc}, data.Accounts...)
	}

	// Map old GUIDs to new GUIDs to prevent primary key collisions when sharing files
	accountMap := make(map[string]string)
	for i, acc := range data.Accounts {
		newGUID := uuid.New().String()
		accountMap[acc.GUID] = newGUID
		data.Accounts[i].GUID = newGUID
	}
	for i, acc := range data.Accounts {
		if acc.ParentGUID != nil {
			if mapped, ok := accountMap[*acc.ParentGUID]; ok {
				m := mapped
				data.Accounts[i].ParentGUID = &m
			}
		}
	}
	for i, t := range data.Transactions {
		data.Transactions[i].GUID = uuid.New().String()
		for j, s := range t.Splits {
			data.Transactions[i].Splits[j].GUID = uuid.New().String()
			if mapped, ok := accountMap[s.AccountGUID]; ok {
				data.Transactions[i].Splits[j].AccountGUID = mapped
			}
		}
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Secure deletion: only deletes records associated with the user's book
	// Clear book root first to avoid FK constraint
	_, err = tx.Exec(r.Context(), "UPDATE books SET root_account_guid = NULL WHERE guid = $1", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear book root")
		return
	}

	_, err = tx.Exec(r.Context(), "DELETE FROM splits WHERE tx_guid IN (SELECT guid FROM transactions WHERE book_guid = $1)", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up splits")
		return
	}
	_, err = tx.Exec(r.Context(), "DELETE FROM transactions WHERE book_guid = $1", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up transactions")
		return
	}
	_, err = tx.Exec(r.Context(), "DELETE FROM accounts WHERE book_guid = $1", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up accounts")
		return
	}

	var rootAccountGUID string
	// Pass 1: Insert accounts without parent_guid to avoid FK issues
	for _, acc := range data.Accounts {
		if acc.AccountType == "ROOT" {
			rootAccountGUID = acc.GUID
		}
		_, err = tx.Exec(r.Context(),
			`INSERT INTO accounts (guid, name, account_type, parent_guid, book_guid, placeholder, description)
			 VALUES ($1, $2, $3, NULL, $4, $5, $6)`,
			acc.GUID, acc.Name, acc.AccountType, bookGUID, acc.Placeholder, acc.Description)
		if err != nil {
			log.Printf("Error inserting account (pass 1): %v", err)
			writeError(w, http.StatusInternalServerError, "failed to insert account")
			return
		}
	}

	// Pass 2: Update accounts with their parent_guid
	for _, acc := range data.Accounts {
		if acc.ParentGUID != nil {
			_, err = tx.Exec(r.Context(),
				`UPDATE accounts SET parent_guid = $1 WHERE guid = $2 AND book_guid = $3`,
				acc.ParentGUID, acc.GUID, bookGUID)
			if err != nil {
				log.Printf("Error updating account parent (pass 2): %v", err)
				writeError(w, http.StatusInternalServerError, "failed to update account hierarchy")
				return
			}
		}
	}

	if rootAccountGUID != "" {
		_, err = tx.Exec(r.Context(), "UPDATE books SET root_account_guid = $1 WHERE guid = $2", rootAccountGUID, bookGUID)
		if err != nil {
			log.Printf("Error updating book root: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to restore book root")
			return
		}
	}

	for _, t := range data.Transactions {
		_, err = tx.Exec(r.Context(),
			`INSERT INTO transactions (guid, book_guid, post_date, enter_date, description)
			 VALUES ($1, $2, $3, $4, $5)`,
			t.GUID, bookGUID, t.PostDate, t.EnterDate, t.Description)
		if err != nil {
			log.Printf("Error inserting transaction: %v", err)
			writeError(w, http.StatusInternalServerError, "failed to insert transaction")
			return
		}

		for _, s := range t.Splits {
			_, err = tx.Exec(r.Context(),
				`INSERT INTO splits (guid, tx_guid, account_guid, memo, value_num, value_denom, quantity_num, quantity_denom, reconcile_state)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				s.GUID, t.GUID, s.AccountGUID, s.Memo, s.ValueNum, s.ValueDenom, s.QuantityNum, s.QuantityDenom, s.ReconcileState)
			if err != nil {
				log.Printf("Error inserting split: %v", err)
				writeError(w, http.StatusInternalServerError, "failed to insert split")
				return
			}
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "import successful"})
}

func (h *ImportExportHandler) handleExport(w http.ResponseWriter, r *http.Request) {
	bookGUID := auth.BookGUIDFromCtx(r.Context())
	if bookGUID == "" {
		writeError(w, http.StatusUnauthorized, "missing book guid")
		return
	}

	var data ExportData

	rows, err := h.pool.Query(r.Context(),
		`SELECT guid, name, account_type, parent_guid, placeholder, description 
		 FROM accounts WHERE book_guid = $1
		 ORDER BY parent_guid ASC NULLS FIRST`, bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to export accounts")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var acc ExportAccount
		if err := rows.Scan(&acc.GUID, &acc.Name, &acc.AccountType, &acc.ParentGUID, &acc.Placeholder, &acc.Description); err != nil {
			writeError(w, http.StatusInternalServerError, "error reading accounts")
			return
		}
		data.Accounts = append(data.Accounts, acc)
	}
	rows.Close()

	txRows, err := h.pool.Query(r.Context(),
		`SELECT guid, cast(post_date as varchar), cast(enter_date as varchar), description 
		 FROM transactions WHERE book_guid = $1`, bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to export txs")
		return
	}
	defer txRows.Close()

	for txRows.Next() {
		var tx ExportTransaction
		var postDate, enterDate string
		if err := txRows.Scan(&tx.GUID, &postDate, &enterDate, &tx.Description); err != nil {
			continue
		}
		tx.PostDate = postDate
		tx.EnterDate = enterDate

		spRows, err := h.pool.Query(context.Background(),
			`SELECT guid, account_guid, memo, value_num, value_denom, quantity_num, quantity_denom, reconcile_state 
			 FROM splits WHERE tx_guid = $1`, tx.GUID)
		if err == nil {
			for spRows.Next() {
				var s ExportSplit
				if err := spRows.Scan(&s.GUID, &s.AccountGUID, &s.Memo, &s.ValueNum, &s.ValueDenom, &s.QuantityNum, &s.QuantityDenom, &s.ReconcileState); err == nil {
					tx.Splits = append(tx.Splits, s)
				}
			}
			spRows.Close()
		}
		data.Transactions = append(data.Transactions, tx)
	}
	txRows.Close()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"export.json\"")
	writeJSON(w, http.StatusOK, data)
}
