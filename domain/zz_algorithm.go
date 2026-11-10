package domain

import (
	"errors"
	"math/big"

	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/units"
)

// DimensionalWeight is volume * weight-unit ticks / (length-unit ticks^3 * divisor), floored:
// the weight a carrier bills a box of these dimensions at. An overflow of int64 panics with
// ints.Overflow, as the reference's exact conversion throws.
func (d Dimensions) DimensionalWeight(divisor int64, lengthUnit, weightUnit string) (units.Weight, error) {
	if divisor <= 0 {
		return units.Weight{}, errors.New("divisor must be positive")
	}
	lengthTicks, err := units.LengthUnitTicks(lengthUnit)
	if err != nil {
		return units.Weight{}, err
	}
	weightTicks, err := units.WeightUnitTicks(weightUnit)
	if err != nil {
		return units.Weight{}, err
	}
	numerator := new(big.Int).Mul(d.Volume(), big.NewInt(weightTicks))
	perUnit := big.NewInt(lengthTicks)
	denominator := new(big.Int).Mul(perUnit, perUnit)
	denominator.Mul(denominator, perUnit).Mul(denominator, big.NewInt(divisor))
	quotient := numerator.Quo(numerator, denominator)
	if !quotient.IsInt64() {
		panic(ints.Overflow{Operation: "dimensional weight"})
	}
	return units.NewWeight(quotient.Int64())
}
