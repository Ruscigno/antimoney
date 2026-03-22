package gnc

import (
	"testing"
)

func TestNew(t *testing.T) {
	n := New(100, 100)
	if n.Num != 100 || n.Denom != 100 {
		t.Errorf("expected 100/100, got %s", n)
	}

	// Negative denom should be normalized
	n = New(5, -3)
	if n.Num != -5 || n.Denom != 3 {
		t.Errorf("expected -5/3, got %s", n)
	}
}

func TestNewPanicsOnZeroDenom(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on zero denom")
		}
	}()
	New(1, 0)
}

func TestAdd(t *testing.T) {
	a := New(1050, 100) // 10.50
	b := New(2575, 100) // 25.75
	got := a.Add(b)

	// 10.50 + 25.75 = 36.25 = 145/4 (big.Rat auto-reduces)
	if got.ToFloat64() != 36.25 {
		t.Errorf("Add value: got %f, want 36.25", got.ToFloat64())
	}
	expected := New(145, 4)
	if !got.Equal(expected) {
		t.Errorf("Add: got %s, want %s", got, expected)
	}
}

func TestSub(t *testing.T) {
	a := New(2575, 100) // 25.75
	b := New(1050, 100) // 10.50
	got := a.Sub(b)

	if got.ToFloat64() != 15.25 {
		t.Errorf("Sub: got %f, want 15.25", got.ToFloat64())
	}
}

func TestMul(t *testing.T) {
	a := New(10, 1)    // 10
	b := New(550, 100) // 5.50
	got := a.Mul(b)

	if got.ToFloat64() != 55.0 {
		t.Errorf("Mul: got %f, want 55.0", got.ToFloat64())
	}
}

func TestDiv(t *testing.T) {
	a := New(10, 1) // 10
	b := New(3, 1)  // 3
	got, err := a.Div(b)
	if err != nil {
		t.Fatal(err)
	}

	// 10/3 should be exact rational, not truncated
	want := New(10, 3)
	if !got.Equal(want) {
		t.Errorf("Div: got %s, want %s", got, want)
	}
}

func TestDivByZero(t *testing.T) {
	a := New(10, 1)
	_, err := a.Div(Zero())
	if err != ErrDivideByZero {
		t.Errorf("expected ErrDivideByZero, got %v", err)
	}
}

func TestConvertBankerRounding(t *testing.T) {
	tests := []struct {
		name    string
		input   Numeric
		denom   int64
		wantNum int64
	}{
		{"exact", New(1, 4), 100, 25},                 // 0.25 => 25/100
		{"round down", New(1, 3), 100, 33},            // 0.333... => 33/100
		{"round up", New(2, 3), 100, 67},              // 0.666... => 67/100
		{"half to even (even)", New(5, 1000), 100, 0}, // 0.005 => 0/100 (round to even 0)
		{"half to even (odd)", New(15, 1000), 100, 2}, // 0.015 => 2/100 (round to even 2)
		{"half to even (25)", New(25, 1000), 100, 2},  // 0.025 => 2/100
		{"half to even (35)", New(35, 1000), 100, 4},  // 0.035 => 4/100
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.input.Convert(tt.denom, RoundBanker)
			if err != nil {
				t.Fatal(err)
			}
			if got.Num != tt.wantNum || got.Denom != tt.denom {
				t.Errorf("got %d/%d, want %d/%d", got.Num, got.Denom, tt.wantNum, tt.denom)
			}
		})
	}
}

func TestConvertRoundNever(t *testing.T) {
	// Exact conversion should work
	n := New(1, 4) // 0.25
	got, err := n.Convert(100, RoundNever)
	if err != nil {
		t.Fatal(err)
	}
	if got.Num != 25 || got.Denom != 100 {
		t.Errorf("got %s, want 25/100", got)
	}

	// Inexact should error
	n = New(1, 3)
	_, err = n.Convert(100, RoundNever)
	if err != ErrRemainder {
		t.Errorf("expected ErrRemainder, got %v", err)
	}
}

func TestConvertTruncate(t *testing.T) {
	// 1/3 truncated to denom 100 => 33/100
	n := New(1, 3)
	got, err := n.Convert(100, RoundTruncate)
	if err != nil {
		t.Fatal(err)
	}
	if got.Num != 33 {
		t.Errorf("got %d/100, want 33/100", got.Num)
	}

	// Negative: -1/3 truncated => -33/100 (toward zero)
	n = New(-1, 3)
	got, err = n.Convert(100, RoundTruncate)
	if err != nil {
		t.Fatal(err)
	}
	if got.Num != -33 {
		t.Errorf("negative truncate: got %d/100, want -33/100", got.Num)
	}
}

func TestConvertFloor(t *testing.T) {
	// 1/3 floor to denom 100 => 33/100
	n := New(1, 3)
	got, err := n.Convert(100, RoundFloor)
	if err != nil {
		t.Fatal(err)
	}
	if got.Num != 33 {
		t.Errorf("got %d/100, want 33/100", got.Num)
	}

	// Negative: -1/3 floor => -34/100 (toward -inf)
	n = New(-1, 3)
	got, err = n.Convert(100, RoundFloor)
	if err != nil {
		t.Fatal(err)
	}
	if got.Num != -34 {
		t.Errorf("negative floor: got %d/100, want -34/100", got.Num)
	}
}

func TestIsZero(t *testing.T) {
	if !Zero().IsZero() {
		t.Error("Zero should be zero")
	}
	if New(1, 100).IsZero() {
		t.Error("1/100 should not be zero")
	}
}

func TestNeg(t *testing.T) {
	n := New(500, 100)
	neg := n.Neg()
	if neg.Num != -500 || neg.Denom != 100 {
		t.Errorf("got %s, want -500/100", neg)
	}
}

func TestReduce(t *testing.T) {
	n := New(500, 100) // 5/1
	r := n.Reduce()
	if r.Num != 5 || r.Denom != 1 {
		t.Errorf("got %s, want 5/1", r)
	}
}

func TestSumIsZero(t *testing.T) {
	// Balanced transaction: checking -500, savings +300, expense +200
	splits := []Numeric{
		New(-50000, 100),
		New(30000, 100),
		New(20000, 100),
	}
	if !SumIsZero(splits) {
		t.Error("balanced splits should sum to zero")
	}

	// Unbalanced
	splits = []Numeric{
		New(-50000, 100),
		New(30000, 100),
	}
	if SumIsZero(splits) {
		t.Error("unbalanced splits should not sum to zero")
	}
}

func TestFromAmount(t *testing.T) {
	n, err := FromAmount("10.50", 100)
	if err != nil {
		t.Fatal(err)
	}
	if n.Num != 1050 || n.Denom != 100 {
		t.Errorf("got %s, want 1050/100", n)
	}

	// Invalid amount for given SCU
	_, err = FromAmount("10.555", 100)
	if err == nil {
		t.Error("expected error for amount that can't be represented with SCU 100")
	}
}

func TestEqual(t *testing.T) {
	a := New(1, 2)
	b := New(500, 1000)
	if !a.Equal(b) {
		t.Errorf("%s should equal %s", a, b)
	}
}

func TestFormatDecimal(t *testing.T) {
	n := New(1050, 100) // 10.50
	s := FormatDecimal(n, 2)
	if s != "10.50" {
		t.Errorf("got %s, want 10.50", s)
	}
}
