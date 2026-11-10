package units

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Quantity is a caller-supplied magnitude before it becomes ticks: an exact integer or
// text (a decimal, a fraction or a mixed fraction). Binary floating point is not a
// Quantity, which is how float input stays refused at the type level.
type Quantity struct {
	integer *big.Int
	text    string
}

// IntQuantity is an exact integer magnitude.
func IntQuantity(value int64) Quantity { return Quantity{integer: big.NewInt(value)} }

// BigQuantity is an exact integer magnitude of any size.
func BigQuantity(value *big.Int) Quantity { return Quantity{integer: new(big.Int).Set(value)} }

// TextQuantity is a decimal, fraction or mixed-fraction magnitude, optionally carrying a unit
// when parsed through ParseLength or ParseWeight.
func TextQuantity(text string) Quantity { return Quantity{text: text} }

// IsText reports whether the quantity was supplied as text.
func (q Quantity) IsText() bool { return q.integer == nil }

// Text returns the quantity's text form; empty for an integer quantity.
func (q Quantity) Text() string { return q.text }

// MaxFractionDigits is the shared cap on written fractional digits. PHP cannot hold 10^19
// in an integer, and a silently different accepted range between engines is the
// divergence this library exists to avoid. Eighteen digits is far past what a tick can
// express.
const MaxFractionDigits = 18

// maxDecimalExponent mirrors the reference decimal context's exponent range, so an
// exponent no reference operation could carry is refused instead of expanded.
const maxDecimalExponent = 999_999

var (
	mixedPattern  = regexp.MustCompile(`^([+-]?` + digitClass + `+)` + spaceClass + `+(` + digitClass + `+)` + spaceClass + `*/` + spaceClass + `*(` + digitClass + `+)$`)
	simplePattern = regexp.MustCompile(`^([+-]?` + digitClass + `+)` + spaceClass + `*/` + spaceClass + `*(` + digitClass + `+)$`)
	plainDecimal  = regexp.MustCompile(`^[+-]?` + digitClass + `+(?:\.(` + digitClass + `+))?$`)
	decimalSyntax = regexp.MustCompile(`^([+-]?)(?:([0-9]+)(?:\.([0-9]*))?|\.([0-9]+))(?:[eE]([+-]?[0-9]+))?$`)
	specialSyntax = regexp.MustCompile(`(?i)^[+-]?(?:inf|infinity|s?nan[0-9]*)$`)
)

// ErrDecimalSyntax reports text that is not a number at all.
var ErrDecimalSyntax = errors.New("[<class 'decimal.ConversionSyntax'>]")

// Rational returns the exact value of a quantity.
func (q Quantity) Rational() (*big.Rat, error) {
	if !q.IsText() {
		return new(big.Rat).SetInt(q.integer), nil
	}
	return parseRational(q.text)
}

func parseRational(raw string) (*big.Rat, error) {
	text := strings.ReplaceAll(stripReferenceSpace(raw), " ", " ")
	if groups := mixedPattern.FindStringSubmatch(text); groups != nil {
		whole, numerator, denominator := mustInt(groups[1]), mustInt(groups[2]), mustInt(groups[3])
		// The sign comes from the written text, not from the value of the whole part, so
		// "-0 1/2" is minus one half rather than plus one half.
		magnitude := new(big.Int).Mul(new(big.Int).Abs(whole), denominator)
		magnitude.Add(magnitude, numerator)
		if strings.HasPrefix(text, "-") {
			magnitude.Neg(magnitude)
		}
		return fraction(magnitude, denominator)
	}
	if groups := simplePattern.FindStringSubmatch(text); groups != nil {
		return fraction(mustInt(groups[1]), mustInt(groups[2]))
	}
	if groups := plainDecimal.FindStringSubmatch(text); groups != nil && len([]rune(groups[1])) > MaxFractionDigits {
		return nil, errors.New("decimal has more fractional digits than a tick can distinguish")
	}
	return parseDecimal(text)
}

func fraction(numerator, denominator *big.Int) (*big.Rat, error) {
	if denominator.Sign() == 0 {
		return nil, fmt.Errorf("Fraction(%s, 0)", numerator)
	}
	return new(big.Rat).SetFrac(numerator, denominator), nil
}

func mustInt(digits string) *big.Int {
	value, ok := new(big.Int).SetString(asciiDigits(digits), 10)
	if !ok {
		panic("units: digit run did not parse: " + digits)
	}
	return value
}

// parseDecimal reads a decimal literal the way the reference decimal constructor does:
// surrounding whitespace and every underscore are ignored, any Unicode decimal digit
// counts, and infinities and NaNs parse but have no exact value.
func parseDecimal(raw string) (*big.Rat, error) {
	text := asciiDigits(strings.ReplaceAll(stripReferenceSpace(raw), "_", ""))
	if specialSyntax.MatchString(text) {
		return nil, fmt.Errorf("cannot convert %s to an exact rational", text)
	}
	groups := decimalSyntax.FindStringSubmatch(text)
	if groups == nil {
		return nil, ErrDecimalSyntax
	}
	integerDigits, fractionDigits := groups[2], groups[3]
	if groups[4] != "" {
		fractionDigits = groups[4]
	}
	exponent := 0
	if groups[5] != "" {
		parsed, ok := new(big.Int).SetString(groups[5], 10)
		if !ok || !parsed.IsInt64() || parsed.Int64() > maxDecimalExponent || parsed.Int64() < -maxDecimalExponent {
			return nil, errors.New("decimal exponent out of range")
		}
		exponent = int(parsed.Int64())
	}
	coefficient := mustInt(integerDigits + fractionDigits)
	if groups[1] == "-" {
		coefficient.Neg(coefficient)
	}
	return scaleByPowerOfTen(coefficient, exponent-len(fractionDigits)), nil
}

func scaleByPowerOfTen(coefficient *big.Int, exponent int) *big.Rat {
	power := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(exponent))), nil)
	if exponent >= 0 {
		return new(big.Rat).SetInt(new(big.Int).Mul(coefficient, power))
	}
	return new(big.Rat).SetFrac(coefficient, power)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
