package units

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"unicode"
)

// scale is one physical quantity's unit table: ticks per named unit.
type scale struct {
	name        string
	multipliers map[string]int64
	suffix      *regexp.Regexp
}

func newScale(name string, multipliers map[string]int64, suffixAlternation string) scale {
	pattern := `(?i)^(.+?)` + spaceClass + `*(` + suffixAlternation + `)$`
	return scale{name: name, multipliers: multipliers, suffix: regexp.MustCompile(pattern)}
}

// multiplier resolves a unit name; `strict` keeps surrounding whitespace significant, which
// is how the reference reads output units as opposed to input units.
func (s scale) multiplier(unit string, strict bool) (int64, error) {
	key := strings.Map(unicode.ToLower, unit)
	if !strict {
		key = stripReferenceSpace(key)
	}
	value, ok := s.multipliers[key]
	if !ok {
		return 0, fmt.Errorf("unsupported %s unit: %s", s.name, unit)
	}
	return value, nil
}

// ticks converts an exact quantity in `unit` to ticks.
func (s scale) ticks(quantity Quantity, unit string, rounding Rounding) (int64, error) {
	multiplier, err := s.multiplier(unit, false)
	if err != nil {
		return 0, err
	}
	value, err := quantity.Rational()
	if err != nil {
		return 0, err
	}
	value.Mul(value, new(big.Rat).SetInt64(multiplier))
	rounded := Round(value, rounding)
	if rounded.Sign() < 0 {
		return 0, fmt.Errorf("%s cannot be negative", s.name)
	}
	if !rounded.IsInt64() {
		return 0, fmt.Errorf("%s exceeds the 64-bit tick range", s.name)
	}
	return rounded.Int64(), nil
}

// parse reads a quantity that may carry its own unit suffix ("12 3/8 in", "2.5kg").
func (s scale) parse(quantity Quantity, defaultUnit string, rounding Rounding) (int64, error) {
	if quantity.IsText() {
		groups := s.suffix.FindStringSubmatch(stripReferenceSpace(quantity.Text()))
		if groups != nil {
			return s.ticks(TextQuantity(stripReferenceSpace(groups[1])), groups[2], rounding)
		}
	}
	return s.ticks(quantity, defaultUnit, rounding)
}

// render is the `value` string of `ticks` expressed in `unit`.
func (s scale) render(ticks int64, unit string) (string, error) {
	multiplier, err := s.multiplier(unit, true)
	if err != nil {
		return "", err
	}
	return RenderDecimal(big.NewInt(ticks), big.NewInt(multiplier), DefaultPlaces), nil
}
