package units

import (
	"fmt"
	"math/big"
)

// Rounding chooses how an exact value becomes a whole number of ticks.
type Rounding string

const (
	Floor   Rounding = "floor"
	Ceil    Rounding = "ceil"
	Nearest Rounding = "nearest"
)

// ParseRounding reads one of the three rounding names.
func ParseRounding(name string) (Rounding, error) {
	switch rounding := Rounding(name); rounding {
	case Floor, Ceil, Nearest:
		return rounding, nil
	}
	return "", fmt.Errorf("'%s' is not a valid Rounding", name)
}

// Round turns an exact value into an integer: floor, ceiling, or nearest with ties to
// even.
func Round(value *big.Rat, rounding Rounding) *big.Int {
	numerator, denominator := value.Num(), value.Denom()
	quotient, remainder := new(big.Int).QuoRem(new(big.Int).Abs(numerator), denominator, new(big.Int))
	negative := numerator.Sign() < 0
	if remainder.Sign() == 0 {
		return withSign(quotient, negative)
	}
	switch rounding {
	case Floor:
		if negative {
			return withSign(quotient.Add(quotient, one), true)
		}
		return quotient
	case Ceil:
		if negative {
			return withSign(quotient, true)
		}
		return quotient.Add(quotient, one)
	}
	switch new(big.Int).Lsh(remainder, 1).Cmp(denominator) {
	case -1:
		return withSign(quotient, negative)
	case 1:
		return withSign(quotient.Add(quotient, one), negative)
	}
	if quotient.Bit(0) == 1 {
		quotient.Add(quotient, one)
	}
	return withSign(quotient, negative)
}

var one = big.NewInt(1)

func withSign(magnitude *big.Int, negative bool) *big.Int {
	if negative {
		return magnitude.Neg(magnitude)
	}
	return magnitude
}
