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
	"github.com/user/antimoney/internal/seed"
	"github.com/user/antimoney/internal/services"
	"encoding/csv"
	"strconv"
	"strings"
	"time"
)

type ImportExportHandler struct {
	pool *pgxpool.Pool
	txSvc *services.TransactionService
}

func NewImportExportHandler(pool *pgxpool.Pool, txSvc *services.TransactionService) *ImportExportHandler {
	return &ImportExportHandler{pool: pool, txSvc: txSvc}
}

func (h *ImportExportHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/import", h.handleImport)
	r.Post("/import/csv", h.handleCSVImport)
	r.Get("/export", h.handleExport)
	r.Post("/reset", h.handleFactoryReset)
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

func (h *ImportExportHandler) handleCSVImport(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}

	accountGUID := r.FormValue("account_guid")
	if accountGUID == "" {
		writeError(w, http.StatusBadRequest, "account_guid is required")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// Some CSVs might use semicolon
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		// Try with semicolon if comma failed to produce multiple columns
		file.Seek(0, 0)
		reader = csv.NewReader(file)
		reader.Comma = ';'
		reader.LazyQuotes = true
		reader.TrimLeadingSpace = true
		records, err = reader.ReadAll()
		if err != nil {
			log.Printf("Error reading CSV: %v", err)
			writeError(w, http.StatusBadRequest, "invalid csv format")
			return
		}
	}

	if len(records) < 2 {
		writeJSON(w, http.StatusOK, map[string]string{"message": "no records to import"})
		return
	}

	// Header detection
	header := records[0]
	dateIdx, descIdx, amountIdx := -1, -1, -1

	for i, col := range header {
		colLower := strings.ToLower(col)
		if dateIdx == -1 && (strings.Contains(colLower, "date")) {
			dateIdx = i
		}
		if descIdx == -1 && (strings.Contains(colLower, "description") || strings.Contains(colLower, "memo") || strings.Contains(colLower, "payee")) {
			descIdx = i
		}
		if amountIdx == -1 && (strings.Contains(colLower, "amount num") || strings.EqualFold(colLower, "amount") || strings.Contains(colLower, "value num") || strings.EqualFold(colLower, "value")) {
			amountIdx = i
		}
	}

	// Fallback if header not recognized
	if dateIdx == -1 || amountIdx == -1 {
		dateIdx = 0
		if len(header) > 1 {
			descIdx = 1
		}
		if len(header) > 2 {
			amountIdx = 2
		} else {
			amountIdx = 1
		}
	}

	if descIdx == -1 {
		descIdx = dateIdx // Use date as description if nothing else
	}

	count := 0
	for i := 1; i < len(records); i++ {
		row := records[i]
		if len(row) <= max(dateIdx, max(descIdx, amountIdx)) {
			continue
		}

		dateStr := row[dateIdx]
		description := row[descIdx]
		amountStr := row[amountIdx]

		if dateStr == "" || amountStr == "" {
			continue
		}

		// Clean amount string: remove currency symbols, spaces, handle comma/dot
		amountStr = strings.Map(func(r rune) rune {
			if (r >= '0' && r <= '9') || r == '-' || r == '.' || r == ',' {
				return r
			}
			return -1
		}, amountStr)

		// Heuristic for comma as decimal separator vs thousands separator
		// If there's a comma and a dot, comma is likely thousands
		// If there's only a comma and it's 2 places from end, it's likely decimal
		if strings.Contains(row[amountIdx], ",") && strings.Contains(row[amountIdx], ".") {
			amountStr = strings.Replace(amountStr, ",", "", -1)
		} else if strings.Contains(amountStr, ",") {
			// Check if comma is decimal separator (e.g. 1234,56)
			parts := strings.Split(amountStr, ",")
			if len(parts) == 2 && len(parts[1]) == 2 {
				amountStr = parts[0] + "." + parts[1]
			} else {
				amountStr = strings.Replace(amountStr, ",", "", -1)
			}
		}

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil {
			log.Printf("Error parsing amount at row %d (%s): %v", i+1, amountStr, err)
			continue
		}

		// Try different date formats
		var postDate time.Time
		dateFormats := []string{
			"2006-01-02",
			"02/01/2006",
			"01/02/2006",
			"2006/01/02",
			"02-01-2006",
			"1/2/2006",
			"2/1/2006",
			"2006-01-02 15:04:05",
		}
		for _, format := range dateFormats {
			if t, err := time.Parse(format, dateStr); err == nil {
				postDate = t
				break
			}
		}

		if postDate.IsZero() {
			log.Printf("Error parsing date at row %d (%s)", i+1, dateStr)
			continue
		}

		// Convert amount to GNC numeric (multiplied by 100 for cents)
		valNum := int64(amount * 100)
		valDenom := int64(100)

		req := services.CreateTransactionRequest{
			PostDate:    postDate,
			Description: description,
			Splits: []services.CreateSplitRequest{
				{
					AccountGUID:   accountGUID,
					ValueNum:      valNum,
					ValueDenom:    valDenom,
					QuantityNum:   valNum,
					QuantityDenom: valDenom,
				},
			},
		}

		_, err = h.txSvc.CreateTransaction(r.Context(), req)
		if err != nil {
			log.Printf("Error creating transaction at row %d: %v", i+1, err)
			continue
		}
		count++
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "csv import successful",
		"count":   count,
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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

func (h *ImportExportHandler) handleFactoryReset(w http.ResponseWriter, r *http.Request) {
	bookGUID := auth.BookGUIDFromCtx(r.Context())
	if bookGUID == "" {
		writeError(w, http.StatusUnauthorized, "missing book guid")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Unlink book root to avoid FK constraints
	_, err = tx.Exec(r.Context(), "UPDATE books SET root_account_guid = NULL WHERE guid = $1", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear book root")
		return
	}

	// Delete user splits
	_, err = tx.Exec(r.Context(), "DELETE FROM splits WHERE tx_guid IN (SELECT guid FROM transactions WHERE book_guid = $1)", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up splits")
		return
	}

	// Delete user transactions
	_, err = tx.Exec(r.Context(), "DELETE FROM transactions WHERE book_guid = $1", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up transactions")
		return
	}

	// Delete user accounts
	_, err = tx.Exec(r.Context(), "DELETE FROM accounts WHERE book_guid = $1", bookGUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up accounts")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// Re-seed the default chart of accounts
	if err := seed.SeedUserBook(r.Context(), h.pool, bookGUID); err != nil {
		log.Printf("Failed to re-seed book during factory reset: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to re-seed account")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "account reset successfully"})
}
