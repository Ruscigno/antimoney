package handlers

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestParseGnuCash(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8" ?>
<gnc-v2
     xmlns:gnc="http://www.gnucash.org/XML/gnc"
     xmlns:act="http://www.gnucash.org/XML/act"
     xmlns:trn="http://www.gnucash.org/XML/trn"
     xmlns:ts="http://www.gnucash.org/XML/ts"
     xmlns:split="http://www.gnucash.org/XML/split"
     xmlns:cmdty="http://www.gnucash.org/XML/cmdty"
     xmlns:slot="http://www.gnucash.org/XML/slot">
  <gnc:book version="2.0.0">
    <gnc:account version="2.0.0">
      <act:name>Root Account</act:name>
      <act:id type="guid">159a711b4a9c48c0be67aa5af469fe47</act:id>
      <act:type>ROOT</act:type>
      <act:description>The Top</act:description>
      <act:slots>
        <slot>
          <slot:key>placeholder</slot:key>
          <slot:value type="string">true</slot:value>
        </slot>
      </act:slots>
    </gnc:account>
    <gnc:account version="2.0.0">
      <act:name>Child Account</act:name>
      <act:id type="guid">2222711b4a9c48c0be67aa5af469fe22</act:id>
      <act:type>BANK</act:type>
      <act:parent type="guid">159a711b4a9c48c0be67aa5af469fe47</act:parent>
    </gnc:account>
    <gnc:transaction version="2.0.0">
      <trn:id type="guid">533f79f325124719851cdfa110278749</trn:id>
      <trn:date-posted>
        <ts:date>2022-01-31 10:59:00 +0000</ts:date>
      </trn:date-posted>
      <trn:description>Grocery Store</trn:description>
      <trn:splits>
        <trn:split>
          <split:id type="guid">a9819867dbe0475dbaea504e7f85c0b8</split:id>
          <split:memo>Food</split:memo>
          <split:reconciled-state>y</split:reconciled-state>
          <split:value>155405/100</split:value>
          <split:quantity>155405/100</split:quantity>
          <split:account type="guid">2222711b4a9c48c0be67aa5af469fe22</split:account>
        </trn:split>
        <trn:split>
          <split:id type="guid">bbbb867dbe0475dbaea504e7f85c0b8</split:id>
          <split:value>-155405/100</split:value>
          <split:quantity>-155405/100</split:quantity>
          <split:account type="guid">159a711b4a9c48c0be67aa5af469fe47</split:account>
        </trn:split>
      </trn:splits>
    </gnc:transaction>
  </gnc:book>
</gnc-v2>`

	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(xmlData))
	w.Close()

	data, err := ParseGnuCash(&b)
	if err != nil {
		t.Fatalf("ParseGnuCash failed: %v", err)
	}

	if len(data.Accounts) != 2 {
		t.Errorf("expected 2 accounts, got %d", len(data.Accounts))
	}

	root := data.Accounts[0]
	if root.GUID != "159a711b4a9c48c0be67aa5af469fe47" {
		t.Errorf("unexpected root guid: %s", root.GUID)
	}
	if root.AccountType != "ROOT" {
		t.Errorf("unexpected type: %s", root.AccountType)
	}
	if !root.Placeholder {
		t.Errorf("expected root to be placeholder")
	}
	if root.Description != "The Top" {
		t.Errorf("expected description 'The Top', got %s", root.Description)
	}

	child := data.Accounts[1]
	if child.ParentGUID == nil || *child.ParentGUID != root.GUID {
		t.Errorf("unexpected parent guid for child")
	}

	if len(data.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(data.Transactions))
	}

	tx := data.Transactions[0]
	if tx.GUID != "533f79f325124719851cdfa110278749" {
		t.Errorf("unexpected tx guid: %s", tx.GUID)
	}
	if tx.Description != "Grocery Store" {
		t.Errorf("unexpected tx description: %s", tx.Description)
	}
	if tx.PostDate != "2022-01-31 10:59:00 +0000" {
		t.Errorf("unexpected post date: %s", tx.PostDate)
	}

	if len(tx.Splits) != 2 {
		t.Fatalf("expected 2 splits, got %d", len(tx.Splits))
	}

	sp1 := tx.Splits[0]
	if sp1.ValueNum != 155405 || sp1.ValueDenom != 100 {
		t.Errorf("unexpected split 1 value: %d/%d", sp1.ValueNum, sp1.ValueDenom)
	}
	if sp1.ReconcileState != "y" {
		t.Errorf("unexpected reconcile state: %s", sp1.ReconcileState)
	}

	sp2 := tx.Splits[1]
	if sp2.ValueNum != -155405 || sp2.ValueDenom != 100 {
		t.Errorf("unexpected split 2 value: %d/%d", sp2.ValueNum, sp2.ValueDenom)
	}
}

func TestParseFraction(t *testing.T) {
	num, denom, err := parseFraction("150/100")
	if err != nil || num != 150 || denom != 100 {
		t.Errorf("failed: %d/%d", num, denom)
	}

	num, denom, err = parseFraction("-500")
	if err != nil || num != -500 || denom != 1 {
		t.Errorf("failed implicit denominator: %d/%d", num, denom)
	}

	_, _, err = parseFraction("50/0")
	if err == nil {
		t.Errorf("expected error on zero denominator")
	}
}
