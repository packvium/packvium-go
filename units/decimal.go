package units

import (
	"math/big"
	"strings"
)

// DefaultPlaces is how many fractional digits a rendered `value` keeps.
const DefaultPlaces = 8

// referencePrecision is the significant-digit precision of the reference engine's decimal
// division. Rendering divides at this precision first and only then quantizes, exactly as
// the reference does, so even a value whose two roundings could interact renders the same.
const referencePrecision = 28

// RenderDecimal renders numerator/denominator (numerator >= 0, denominator > 0) to at most
// `places` fractional digits, rounding ties to even, with trailing zeros removed.
//
// Cost is O(d^2) in the digit count d of the operands, which is bounded by the int64 tick
// range; it runs once per rendered field, never inside a solver.
func RenderDecimal(numerator, denominator *big.Int, places int) string {
	if numerator.Sign() == 0 {
		return "0"
	}
	coefficient, exponent := divideToPrecision(numerator, denominator)
	quantized := quantize(coefficient, exponent, places)
	return formatFixed(quantized, places)
}

// divideToPrecision returns c and e with c * 10^e equal to numerator/denominator rounded
// half-even to referencePrecision significant digits.
func divideToPrecision(numerator, denominator *big.Int) (*big.Int, int) {
	shift := referencePrecision - (len(numerator.String()) - len(denominator.String()))
	for {
		scaled := scaledQuotient(numerator, denominator, shift)
		switch digits := len(scaled.String()); {
		case digits > referencePrecision:
			shift--
		case digits < referencePrecision:
			shift++
		default:
			return roundedQuotient(numerator, denominator, shift), -shift
		}
	}
}

// scaledQuotient is floor(numerator * 10^shift / denominator).
func scaledQuotient(numerator, denominator *big.Int, shift int) *big.Int {
	value := scaleByPowerOfTen(numerator, shift)
	return new(big.Int).Quo(value.Num(), new(big.Int).Mul(value.Denom(), denominator))
}

func roundedQuotient(numerator, denominator *big.Int, shift int) *big.Int {
	value := scaleByPowerOfTen(numerator, shift)
	value.Quo(value, new(big.Rat).SetInt(denominator))
	return Round(value, Nearest)
}

// quantize rounds c * 10^e half-even to `places` fractional digits and returns the result
// as an integer count of 10^-places.
func quantize(coefficient *big.Int, exponent, places int) *big.Int {
	shift := exponent + places
	if shift >= 0 {
		return scaleByPowerOfTen(coefficient, shift).Num()
	}
	return Round(scaleByPowerOfTen(coefficient, shift), Nearest)
}

func formatFixed(units *big.Int, places int) string {
	digits := units.String()
	if len(digits) <= places {
		digits = strings.Repeat("0", places-len(digits)+1) + digits
	}
	whole, fractional := digits[:len(digits)-places], strings.TrimRight(digits[len(digits)-places:], "0")
	if fractional == "" {
		return whole
	}
	return whole + "." + fractional
}
