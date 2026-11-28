package units

import "errors"

// Length is an exact fixed-point length. One tick is 1/16000 mm.
type Length struct{ ticks int64 }

const (
	TicksPerMM   int64 = 16_000
	TicksPerInch int64 = 406_400
)

var lengthScale = newScale("length", map[string]int64{
	"tick":        1,
	"ticks":       1,
	"mm":          TicksPerMM,
	"millimeter":  TicksPerMM,
	"millimeters": TicksPerMM,
	"cm":          TicksPerMM * 10,
	"m":           TicksPerMM * 1000,
	"in":          TicksPerInch,
	"inch":        TicksPerInch,
	"inches":      TicksPerInch,
	"ft":          TicksPerInch * 12,
}, `mm|cm|m|in|inch|inches|ft|ticks?`)

// NewLength wraps a tick count, refusing a negative one.
func NewLength(ticks int64) (Length, error) {
	if ticks < 0 {
		return Length{}, errors.New("length cannot be negative")
	}
	return Length{ticks}, nil
}

// LengthOf converts a quantity in a named unit.
func LengthOf(quantity Quantity, unit string, rounding Rounding) (Length, error) {
	ticks, err := lengthScale.ticks(quantity, unit, rounding)
	return Length{ticks}, err
}

// ParseLength reads a quantity that may carry its own unit suffix, falling back to
// defaultUnit.
func ParseLength(quantity Quantity, defaultUnit string, rounding Rounding) (Length, error) {
	ticks, err := lengthScale.parse(quantity, defaultUnit, rounding)
	return Length{ticks}, err
}

// LengthUnitTicks returns how many ticks one named length unit holds.
func LengthUnitTicks(unit string) (int64, error) { return lengthScale.multiplier(unit, false) }

// Ticks is the exact tick count.
func (l Length) Ticks() int64 { return l.ticks }

// Decimal renders the length in `unit` to eight places, ties to even.
func (l Length) Decimal(unit string) (string, error) { return lengthScale.render(l.ticks, unit) }

// MustLength wraps a tick count the caller has already proven non-negative.
func MustLength(ticks int64) Length {
	if ticks < 0 {
		panic("units: negative length")
	}
	return Length{ticks}
}

// ToMap is the wire form {ticks, value, unit}. The unit must already be known valid.
func (l Length) ToMap(unit string) map[string]interface{} {
	value, err := l.Decimal(unit)
	if err != nil {
		panic(err)
	}
	return map[string]interface{}{"ticks": l.ticks, "value": value, "unit": unit}
}

// lengthUnitCodes are the codes the request schema names wherever it asks for a length unit
// by itself (an output unit, a dimensional-weight unit). A measure's own unit may also be
// spelt out ("inch").
var lengthUnitCodes = []string{"mm", "cm", "m", "in", "ft"}

// LengthUnitCodes is a fresh copy of the schema's length unit codes, in its order.
func LengthUnitCodes() []string { return append([]string(nil), lengthUnitCodes...) }

// OutputLengthUnit is the unit a request without output.length_unit is written in: the
// request's own units.length spelt as the schema's output code, case and surrounding space
// ignored and a spelt-out name folded to its code ("Inches" is "in"). A unit no output code
// names, the internal tick, gives "mm", the default of both blocks.
func OutputLengthUnit(requestUnit string) string {
	multiplier, err := LengthUnitTicks(requestUnit)
	if err != nil {
		return "mm"
	}
	for _, code := range lengthUnitCodes {
		if lengthScale.multipliers[code] == multiplier {
			return code
		}
	}
	return "mm"
}

// IsLengthUnitCode reports whether code is one of the schema's length unit codes, exactly.
func IsLengthUnitCode(code string) bool { return isCode(code, lengthUnitCodes) }

func isCode(code string, codes []string) bool {
	for _, candidate := range codes {
		if code == candidate {
			return true
		}
	}
	return false
}
