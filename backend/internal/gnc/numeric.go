// Package gnc implements exact rational-number arithmetic for financial calculations.
// It mirrors GnuCash's gnc_numeric: every value is stored as num/denom (two int64s)
// to eliminate floating-point rounding errors.
package gnc

import (
	"errors"
	"fmt"
	"math"
	"math/big"
)

// RoundingMode specifies how to handle remainders when converting denominators.
type RoundingMode int

const (
	// RoundBanker performs unbiased banker's rounding (round half to even).
	RoundBanker RoundingMode = iota
	// RoundTruncate truncates fractions toward zero.
	RoundTruncate
	// RoundFloor rounds toward negative infinity.
	RoundFloor
	// RoundCeiling rounds toward positive infinity.
	RoundCeiling
	// RoundNever returns an error if exact conversion is impossible.
	RoundNever
)

// ErrRemainder is returned when RoundNever is used and exact conversion is impossible.
var ErrRemainder = errors.New("gnc: exact conversion impossible, remainder exists")

// ErrDivideByZero is returned on division by zero.
var ErrDivideByZero = errors.New("gnc: division by zero")

// Numeric represents an exact rational number as num/denom.
// Both fields are int64 for compact storage and JSON serialization.
// The denominator must always be positive; the sign lives in the numerator.
type Numeric struct {
	Num   int64 `json:"num"`
	Denom int64 `json:"denom"`
}

// Zero returns a zero value with denominator 1.
func Zero() Numeric {
	return Numeric{Num: 0, Denom: 1}
}

// New creates a Numeric from num/denom. Panics if denom == 0.
func New(num, denom int64) Numeric {
	if denom == 0 {
		panic("gnc: denominator cannot be zero")
	}
	// Normalize: denom is always positive
	if denom < 0 {
		num = -num
		denom = -denom
	}
	return Numeric{Num: num, Denom: denom}
}

// FromInt creates a Numeric from an integer value (denom=1).
func FromInt(n int64) Numeric {
	return Numeric{Num: n, Denom: 1}
}

// FromAmount creates a Numeric from a decimal string amount and a SCU (smallest currency unit).
// Example: FromAmount("10.50", 100) => Numeric{1050, 100}
func FromAmount(amount string, scu int64) (Numeric, error) {
	r := new(big.Rat)
	if _, ok := r.SetString(amount); !ok {
		return Zero(), fmt.Errorf("gnc: invalid amount string %q", amount)
	}
	// Multiply by SCU and check it's exact
	r.Mul(r, new(big.Rat).SetInt64(scu))
	if !r.IsInt() {
		return Zero(), fmt.Errorf("gnc: amount %q cannot be exactly represented with SCU %d", amount, scu)
	}
	num := r.Num().Int64()
	return Numeric{Num: num, Denom: scu}, nil
}

// IsZero returns true if this Numeric equals zero.
func (n Numeric) IsZero() bool {
	return n.Num == 0
}

// IsNegative returns true if this Numeric is less than zero.
func (n Numeric) IsNegative() bool {
	return n.Num < 0
}

// Neg returns the negation of this Numeric.
func (n Numeric) Neg() Numeric {
	return Numeric{Num: -n.Num, Denom: n.Denom}
}

// Abs returns the absolute value.
func (n Numeric) Abs() Numeric {
	if n.Num < 0 {
		return Numeric{Num: -n.Num, Denom: n.Denom}
	}
	return n
}

// toBigRat converts to math/big.Rat for exact intermediate calculations.
func (n Numeric) toBigRat() *big.Rat {
	return new(big.Rat).SetFrac64(n.Num, n.Denom)
}

// Add returns n + other as an exact rational number.
func (n Numeric) Add(other Numeric) Numeric {
	r := new(big.Rat).Add(n.toBigRat(), other.toBigRat())
	return fromBigRat(r)
}

// Sub returns n - other as an exact rational number.
func (n Numeric) Sub(other Numeric) Numeric {
	r := new(big.Rat).Sub(n.toBigRat(), other.toBigRat())
	return fromBigRat(r)
}

// Mul returns n * other as an exact rational number.
func (n Numeric) Mul(other Numeric) Numeric {
	r := new(big.Rat).Mul(n.toBigRat(), other.toBigRat())
	return fromBigRat(r)
}

// Div returns n / other. Returns error if other is zero.
func (n Numeric) Div(other Numeric) (Numeric, error) {
	if other.IsZero() {
		return Zero(), ErrDivideByZero
	}
	r := new(big.Rat).Quo(n.toBigRat(), other.toBigRat())
	return fromBigRat(r), nil
}

// Convert forces this Numeric into the given target denominator using the
// specified rounding mode. This is the core operation for currency display.
// Example: Numeric{1, 3}.Convert(100, RoundBanker) => Numeric{33, 100}  (i.e. 0.33)
func (n Numeric) Convert(targetDenom int64, mode RoundingMode) (Numeric, error) {
	if targetDenom <= 0 {
		return Zero(), fmt.Errorf("gnc: target denominator must be positive, got %d", targetDenom)
	}

	// result = (num * targetDenom) / denom
	bigNum := new(big.Int).Mul(big.NewInt(n.Num), big.NewInt(targetDenom))
	bigDenom := big.NewInt(n.Denom)

	quo, rem := new(big.Int).DivMod(bigNum, bigDenom, new(big.Int))

	if rem.Sign() == 0 {
		return Numeric{Num: quo.Int64(), Denom: targetDenom}, nil
	}

	switch mode {
	case RoundNever:
		return Zero(), ErrRemainder

	case RoundTruncate:
		// Truncate toward zero — DivMod already truncates for positive; handle negative
		if bigNum.Sign() < 0 && rem.Sign() != 0 {
			quo.Add(quo, big.NewInt(1)) // DivMod goes toward -inf, adjust toward zero
		}
		return Numeric{Num: quo.Int64(), Denom: targetDenom}, nil

	case RoundFloor:
		// Floor: toward negative infinity — DivMod already does this
		if bigNum.Sign() < 0 && rem.Sign() != 0 {
			// DivMod truncates, so floor for negative means we keep as-is
			// Actually Go's DivMod does Euclidean division, so quo already floors
		}
		return Numeric{Num: quo.Int64(), Denom: targetDenom}, nil

	case RoundCeiling:
		// Ceiling: toward positive infinity
		if rem.Sign() > 0 {
			quo.Add(quo, big.NewInt(1))
		}
		return Numeric{Num: quo.Int64(), Denom: targetDenom}, nil

	case RoundBanker:
		return bankerRound(quo, rem, bigDenom, targetDenom), nil

	default:
		return Zero(), fmt.Errorf("gnc: unknown rounding mode %d", mode)
	}
}

// bankerRound implements banker's rounding (round half to even).
func bankerRound(quo, rem, denom *big.Int, targetDenom int64) Numeric {
	// Compare 2*|rem| with |denom|
	absRem := new(big.Int).Abs(rem)
	doubled := new(big.Int).Mul(absRem, big.NewInt(2))
	absDenom := new(big.Int).Abs(denom)

	cmp := doubled.Cmp(absDenom)

	sign := int64(1)
	if rem.Sign() < 0 {
		sign = -1
	}

	switch {
	case cmp < 0:
		// Remainder < half: truncate (keep quo)
	case cmp > 0:
		// Remainder > half: round away from zero
		quo.Add(quo, big.NewInt(sign))
	default:
		// Exactly half: round to even
		if new(big.Int).And(quo, big.NewInt(1)).Sign() != 0 {
			// quo is odd, round away from zero
			quo.Add(quo, big.NewInt(sign))
		}
	}

	return Numeric{Num: quo.Int64(), Denom: targetDenom}
}

// Equal returns true if two Numerics represent the same rational value.
func (n Numeric) Equal(other Numeric) bool {
	return n.toBigRat().Cmp(other.toBigRat()) == 0
}

// Cmp compares n to other: -1 if n < other, 0 if equal, +1 if n > other.
func (n Numeric) Cmp(other Numeric) int {
	return n.toBigRat().Cmp(other.toBigRat())
}

// ToFloat64 returns the float64 representation (for display only, NOT for calculations).
func (n Numeric) ToFloat64() float64 {
	if n.Denom == 0 {
		return 0
	}
	f, _ := n.toBigRat().Float64()
	return f
}

// String returns a human-readable string like "1050/100".
func (n Numeric) String() string {
	return fmt.Sprintf("%d/%d", n.Num, n.Denom)
}

// Reduce simplifies the fraction to its lowest terms.
func (n Numeric) Reduce() Numeric {
	if n.Num == 0 {
		return Numeric{Num: 0, Denom: 1}
	}
	g := gcd(abs64(n.Num), abs64(n.Denom))
	return Numeric{Num: n.Num / g, Denom: n.Denom / g}
}

// fromBigRat converts a big.Rat back to Numeric, keeping reduced form.
func fromBigRat(r *big.Rat) Numeric {
	num := r.Num().Int64()
	denom := r.Denom().Int64()
	if denom < 0 {
		num = -num
		denom = -denom
	}
	return Numeric{Num: num, Denom: denom}
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// Sum returns the sum of a slice of Numerics.
func Sum(values []Numeric) Numeric {
	result := Zero()
	for _, v := range values {
		result = result.Add(v)
	}
	return result
}

// SumIsZero returns true if the sum of the given values equals zero.
// This is the core invariant check for double-entry transactions.
func SumIsZero(values []Numeric) bool {
	return Sum(values).IsZero()
}

// RoundToSCU rounds a Numeric to a given smallest currency unit using banker's rounding.
func RoundToSCU(n Numeric, scu int64) (Numeric, error) {
	return n.Convert(scu, RoundBanker)
}

// FormatDecimal formats the Numeric as a decimal string with the given number of decimal places.
func FormatDecimal(n Numeric, decimals int) string {
	f := n.ToFloat64()
	pow := math.Pow10(decimals)
	// Use the float only for display
	rounded := math.Round(f*pow) / pow
	return fmt.Sprintf("%.*f", decimals, rounded)
}
