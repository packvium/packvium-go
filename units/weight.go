package units

import "errors"

// Weight is an exact weight. One tick is 1/8 microgram.
type Weight struct{ ticks int64 }

const (
	TicksPerMG int64 = 8_000
	TicksPerG  int64 = 8_000_000
	TicksPerKG int64 = 8_000_000_000
	TicksPerOZ int64 = 226_796_185
	TicksPerLB int64 = 3_628_738_960
)

var weightScale = newScale("weight", map[string]int64{
	"tick":  1,
	"ticks": 1,
	"mg":    TicksPerMG,
	"g":     TicksPerG,
	"kg":    TicksPerKG,
	"oz":    TicksPerOZ,
	"lb":    TicksPerLB,
	"lbs":   TicksPerLB,
}, `mg|kg|g|oz|lbs?|ticks?`)

// NewWeight wraps a tick count, refusing a negative one.
func NewWeight(ticks int64) (Weight, error) {
	if ticks < 0 {
		return Weight{}, errors.New("weight cannot be negative")
	}
	return Weight{ticks}, nil
}

// WeightOf converts a quantity in a named unit.
func WeightOf(quantity Quantity, unit string, rounding Rounding) (Weight, error) {
	ticks, err := weightScale.ticks(quantity, unit, rounding)
	return Weight{ticks}, err
}

// ParseWeight reads a quantity that may carry its own unit suffix, falling back to
// defaultUnit.
func ParseWeight(quantity Quantity, defaultUnit string, rounding Rounding) (Weight, error) {
	ticks, err := weightScale.parse(quantity, defaultUnit, rounding)
	return Weight{ticks}, err
}

// WeightUnitTicks returns how many ticks one named weight unit holds.
func WeightUnitTicks(unit string) (int64, error) { return weightScale.multiplier(unit, false) }

// Ticks is the exact tick count.
func (w Weight) Ticks() int64 { return w.ticks }

// Decimal renders the weight in `unit` to eight places, ties to even.
func (w Weight) Decimal(unit string) (string, error) { return weightScale.render(w.ticks, unit) }

// MustWeight wraps a tick count the caller has already proven non-negative.
func MustWeight(ticks int64) Weight {
	if ticks < 0 {
		panic("units: negative weight")
	}
	return Weight{ticks}
}

// ToMap is the wire form {ticks, value, unit}. The unit must already be known valid.
func (w Weight) ToMap(unit string) map[string]interface{} {
	value, err := w.Decimal(unit)
	if err != nil {
		panic(err)
	}
	return map[string]interface{}{"ticks": w.ticks, "value": value, "unit": unit}
}
