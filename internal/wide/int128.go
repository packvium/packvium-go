// Package wide is a signed 128-bit integer for exact products of two int64 values.
//
// Go has no native 128-bit type. This covers what the hull predicate needs -- products of
// bounded coordinates and their sums -- without allocating, which math/big would do on
// every operation inside a collision loop. Overflow past 128 bits is the caller's bound to
// keep; every user documents why its operands stay inside it.
package wide

import (
	"math/big"
	"math/bits"
)

// Int128 is a two's-complement signed 128-bit integer.
type Int128 struct {
	hi int64
	lo uint64
}

// From widens an int64.
func From(value int64) Int128 {
	return Int128{hi: value >> 63, lo: uint64(value)}
}

// Mul is the exact product of two int64 values.
func Mul(left, right int64) Int128 {
	negative := (left < 0) != (right < 0)
	hi, lo := bits.Mul64(magnitude(left), magnitude(right))
	product := Int128{hi: int64(hi), lo: lo}
	if negative {
		return product.Neg()
	}
	return product
}

func magnitude(value int64) uint64 {
	if value < 0 {
		return uint64(-value)
	}
	return uint64(value)
}

// Add returns a + b.
func (a Int128) Add(b Int128) Int128 {
	lo, carry := bits.Add64(a.lo, b.lo, 0)
	return Int128{hi: a.hi + b.hi + int64(carry), lo: lo}
}

// Sub returns a - b.
func (a Int128) Sub(b Int128) Int128 {
	lo, borrow := bits.Sub64(a.lo, b.lo, 0)
	return Int128{hi: a.hi - b.hi - int64(borrow), lo: lo}
}

// Neg returns -a.
func (a Int128) Neg() Int128 {
	return Int128{}.Sub(a)
}

// Sign is -1, 0 or +1.
func (a Int128) Sign() int {
	switch {
	case a.hi < 0:
		return -1
	case a.hi == 0 && a.lo == 0:
		return 0
	}
	return 1
}

// Cmp is -1, 0 or +1 as a is less than, equal to or greater than b.
func (a Int128) Cmp(b Int128) int {
	switch {
	case a.hi < b.hi:
		return -1
	case a.hi > b.hi:
		return 1
	case a.lo < b.lo:
		return -1
	case a.lo > b.lo:
		return 1
	}
	return 0
}

// Big converts to an arbitrary-precision integer.
func (a Int128) Big() *big.Int {
	value := new(big.Int).SetInt64(a.hi)
	value.Lsh(value, 64)
	return value.Add(value, new(big.Int).SetUint64(a.lo))
}

// Mul64 returns a * b. The caller keeps the product inside 128 bits.
func (a Int128) Mul64(b int64) Int128 {
	negative := (a.Sign() < 0) != (b < 0)
	m := a
	if m.Sign() < 0 {
		m = m.Neg()
	}
	factor := magnitude(b)
	hi, lo := bits.Mul64(m.lo, factor)
	product := Int128{hi: int64(hi + uint64(m.hi)*factor), lo: lo}
	if negative {
		return product.Neg()
	}
	return product
}

// QuoSmall is floor(a / divisor) for a >= 0 and divisor > 0.
func (a Int128) QuoSmall(divisor uint64) Int128 {
	hi, remainder := bits.Div64(0, uint64(a.hi), divisor)
	lo, _ := bits.Div64(remainder, a.lo, divisor)
	return Int128{hi: int64(hi), lo: lo}
}
