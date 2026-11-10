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
