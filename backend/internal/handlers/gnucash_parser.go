package handlers

import (
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type GncV2 struct {
	XMLName xml.Name `xml:"gnc-v2"`
	Book    GncBook  `xml:"book"`
}

type GncBook struct {
	Accounts     []GncAccount     `xml:"account"`
	Transactions []GncTransaction `xml:"transaction"`
}

type GncAccount struct {
	ID          string    `xml:"id"`
	Name        string    `xml:"name"`
	Type        string    `xml:"type"`
	Description string    `xml:"description"`
	Parent      *string   `xml:"parent"`
	Slots       []GncSlot `xml:"slots>slot"`
}

type GncSlot struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type GncTransaction struct {
	ID          string     `xml:"id"`
	DatePosted  string    `xml:"date-posted>date"`
	DateEntered string    `xml:"date-entered>date"`
	Description string     `xml:"description"`
	Splits      []GncSplit `xml:"splits>split"`
}

type GncSplit struct {
	ID             string `xml:"id"`
	Memo           string `xml:"memo"`
	ReconciledState string `xml:"reconciled-state"`
	Value          string `xml:"value"`
	Quantity       string `xml:"quantity"`
	Account        string `xml:"account"`
}

func parseFraction(val string) (num int64, denom int64, err error) {
	if val == "" {
		return 0, 1, nil
	}
	parts := strings.Split(val, "/")
	if len(parts) == 1 {
		num, err = strconv.ParseInt(parts[0], 10, 64)
		denom = 1
		return
	}
	if len(parts) == 2 {
		num, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 1, err
		}
		denom, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 1, err
		}
		if denom == 0 {
			return 0, 1, fmt.Errorf("denominator cannot be zero")
		}
		return
	}
	return 0, 1, fmt.Errorf("invalid fraction format: %s", val)
}

func ParseGnuCash(r io.Reader) (*ExportData, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip: %w", err)
	}
	defer gz.Close()

	var data GncV2
	decoder := xml.NewDecoder(gz)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode xml: %w", err)
	}

	exportData := &ExportData{}
	
	for _, acc := range data.Book.Accounts {
		placeholder := false
		for _, slot := range acc.Slots {
			if slot.Key == "placeholder" && slot.Value == "true" {
				placeholder = true
			}
		}
		exportData.Accounts = append(exportData.Accounts, ExportAccount{
			GUID:        acc.ID,
			Name:        acc.Name,
			AccountType: acc.Type,
			ParentGUID:  acc.Parent,
			Placeholder: placeholder,
			Description: acc.Description,
		})
	}

	for _, tx := range data.Book.Transactions {
		exportTx := ExportTransaction{
			GUID:        tx.ID,
			PostDate:    tx.DatePosted,
			EnterDate:   tx.DateEntered,
			Description: tx.Description,
			Splits:      []ExportSplit{},
		}

		for _, sp := range tx.Splits {
			vNum, vDenom, err := parseFraction(sp.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to parse split value: %w", err)
			}
			qNum, qDenom, err := parseFraction(sp.Quantity)
			if err != nil {
				return nil, fmt.Errorf("failed to parse split quantity: %w", err)
			}

			exportTx.Splits = append(exportTx.Splits, ExportSplit{
				GUID:           sp.ID,
				AccountGUID:    sp.Account,
				Memo:           sp.Memo,
				ValueNum:       vNum,
				ValueDenom:     vDenom,
				QuantityNum:    qNum,
				QuantityDenom:  qDenom,
				ReconcileState: sp.ReconciledState,
			})
		}
		exportData.Transactions = append(exportData.Transactions, exportTx)
	}

	return exportData, nil
}
