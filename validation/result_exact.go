package validation

import (
	"math/big"
)

// Integer arithmetic with the reference's semantics: exact at any size, floor division and
// modulo rounding toward negative infinity. Every result is a fresh value.

func bi(value int64) *big.Int { return big.NewInt(value) }

func add(left, right *big.Int) *big.Int { return new(big.Int).Add(left, right) }

func sub(left, right *big.Int) *big.Int { return new(big.Int).Sub(left, right) }

func mul(left, right *big.Int) *big.Int { return new(big.Int).Mul(left, right) }

func neg(value *big.Int) *big.Int { return new(big.Int).Neg(value) }

func abs(value *big.Int) *big.Int { return new(big.Int).Abs(value) }

func maxInt(left, right *big.Int) *big.Int {
	if left.Cmp(right) >= 0 {
		return left
	}
	return right
}

func minInt(left, right *big.Int) *big.Int {
	if left.Cmp(right) <= 0 {
		return left
	}
	return right
}

// floorDiv is left // right; a zero divisor is the reference's ZeroDivisionError.
func floorQuo(left, right *big.Int) *big.Int {
	if right.Sign() == 0 {
		panic(shapeRefusal("division by zero"))
	}
	quotient, remainder := new(big.Int).QuoRem(left, right, new(big.Int))
	if remainder.Sign() != 0 && remainder.Sign() != right.Sign() {
		quotient.Sub(quotient, bi(1))
	}
	return quotient
}

func mod(left, right *big.Int) *big.Int { return sub(left, mul(floorQuo(left, right), right)) }

// ceilDiv is -(-left // right).
func ceilDiv(left, right *big.Int) *big.Int { return neg(floorQuo(neg(left), right)) }

// gcd is Euclid's, written out: before Go 1.14 big.Int.GCD returns 0 for a zero operand.
func gcd(left, right *big.Int) *big.Int {
	a, b := abs(left), abs(right)
	for b.Sign() != 0 {
		a, b = b, mod(a, b)
	}
	return a
}

func sum(values []*big.Int) *big.Int {
	total := bi(0)
	for _, value := range values {
		total = add(total, value)
	}
	return total
}

// ------------------------------------------------------------------ fractions

// fraction is the reference's Fraction: big.Rat is always reduced with a positive
// denominator, so Num and Denom read the numbers the reference reads.
func fraction(numerator, denominator *big.Int) *big.Rat {
	if denominator.Sign() == 0 {
		panic(shapeRefusal("Fraction with a zero denominator"))
	}
	return new(big.Rat).SetFrac(numerator, denominator)
}

func ratOf(value *big.Int) *big.Rat { return new(big.Rat).SetInt(value) }

// ratFloat is the reference's float(fraction): the nearest double, ties to even.
func ratFloat(value *big.Rat) float64 {
	result, _ := value.Float64()
	return result
}

// ratFloor is numerator // denominator.
func ratFloor(value *big.Rat) *big.Int { return floorQuo(value.Num(), value.Denom()) }
