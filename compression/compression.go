// Package compression is the occupied height of a compressible item under the load resting
// on it. Every value is an exact integer or a reduced rational: pressure is a
// numerator/denominator pair, and the crush limit is decided by cross multiplication.
package compression

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/packvium/packvium-go/internal/ints"
	"github.com/packvium/packvium-go/units"
)

// PPM is the scale a compression ratio is carried at once parsed.
const PPM = 1_000_000

// Standard gravity is exactly 9.80665 m/s^2, kept as an integer ratio.
const (
	standardGravityNumerator   = 980_665
	standardGravityDenominator = 100_000
	pascalsPerKilopascal       = 1_000
	ticksPerMetre              = units.TicksPerMM * 1_000
)

// CrushError reports applied pressure strictly above an item's declared limit.
type CrushError struct{ message string }

func (e *CrushError) Error() string { return e.message }

// Pressure is an exact non-negative pressure in kPa, held as a reduced rational.
type Pressure struct{ numerator, denominator *big.Int }

// ZeroPressure is no load at all.
func ZeroPressure() Pressure { return Pressure{big.NewInt(0), big.NewInt(1)} }

func reducedPressure(numerator, denominator *big.Int) Pressure {
	// Go before 1.14 defines GCD only for positive operands, so zero load is reduced here.
	if numerator.Sign() == 0 {
		return ZeroPressure()
	}
	divisor := new(big.Int).GCD(nil, nil, numerator, denominator)
	return Pressure{new(big.Int).Quo(numerator, divisor), new(big.Int).Quo(denominator, divisor)}
}

// ExceedsKPA compares against a limit by cross multiplication.
func (p Pressure) ExceedsKPA(limit int64) bool {
	return p.numerator.Cmp(new(big.Int).Mul(big.NewInt(limit), p.denominator)) > 0
}

func (p Pressure) String() string { return fmt.Sprintf("%s/%s kPa", p.numerator, p.denominator) }

// AppliedPressure is the pressure of the mass resting above an item over its uncompressed
// footprint.
func AppliedPressure(topLoad units.Weight, footprintAreaTicks2 *big.Int) (Pressure, error) {
	if footprintAreaTicks2.Sign() <= 0 {
		return Pressure{}, errors.New("footprint area must be positive")
	}
	numerator := big.NewInt(topLoad.Ticks())
	numerator.Mul(numerator, big.NewInt(standardGravityNumerator*ticksPerMetre))
	numerator.Mul(numerator, big.NewInt(ticksPerMetre))
	denominator := big.NewInt(units.TicksPerKG)
	denominator.Mul(denominator, big.NewInt(standardGravityDenominator*pascalsPerKilopascal))
	denominator.Mul(denominator, footprintAreaTicks2)
	return reducedPressure(numerator, denominator), nil
}

// EffectiveHeightTicks is the occupied height under load, rounded up and never below one
// tick, so a discrete packer never claims less space than the continuous model allows.
func EffectiveHeightTicks(heightTicks, ratioPPM, maxPressureKPA int64, pressure Pressure) (int64, error) {
	switch {
	case heightTicks <= 0:
		return 0, errors.New("height must be positive")
	case ratioPPM < 0 || ratioPPM > PPM:
		return 0, errors.New("compression ratio must be between zero and one million ppm")
	case maxPressureKPA < 0:
		return 0, errors.New("maximum pressure cannot be negative")
	case pressure.ExceedsKPA(maxPressureKPA):
		return 0, &CrushError{fmt.Sprintf("applied pressure %s exceeds the declared limit of %d kPa", pressure, maxPressureKPA)}
	case maxPressureKPA == 0:
		return heightTicks, nil
	}
	divisor := new(big.Int).Mul(big.NewInt(maxPressureKPA), big.NewInt(PPM))
	divisor.Mul(divisor, pressure.denominator)
	retained := new(big.Int).Sub(divisor, new(big.Int).Mul(big.NewInt(ratioPPM), pressure.numerator))
	height := new(big.Int).Mul(big.NewInt(heightTicks), retained)
	height.Add(height, divisor).Sub(height, big.NewInt(1)).Quo(height, divisor)
	return ints.Max(1, height.Int64()), nil
}

// RatioToPPM applies the public ratio rule floor(ratio * 10^6 + 0.5) once, at the boundary.
func RatioToPPM(ratio float64) (int64, error) {
	if !(ratio >= 0 && ratio <= 1) {
		return 0, errors.New("compression_ratio must be between zero and one")
	}
	// The explicit conversion rounds the product before the addition; without it Go may fuse
	// the two into one multiply-add and round differently from the reference.
	return int64(float64(ratio*PPM) + 0.5), nil
}
