package domain

import (
	"math/big"

	"github.com/packvium/packvium-go/internal/ints"
)

var (
	million  = big.NewInt(1_000_000)
	maxInt64 = new(big.Int).SetInt64(1<<63 - 1)
)

// CentreOfMassOffsetPPM is how far the weighted centre of mass sits from the container's
// centre, in parts per million of the half-extent along the worse horizontal axis. The
// Chebyshev offset keeps the result exact; each item contributes its physical centre, not its
// clearance envelope. O(n).
func CentreOfMassOffsetPPM(inner Dimensions, placements []Placement) int64 {
	totalWeight := new(big.Int)
	for _, placement := range placements {
		totalWeight.Add(totalWeight, big.NewInt(placement.instance.Item().Weight().Ticks()))
	}
	if totalWeight.Sign() == 0 {
		return 0
	}
	doubledX, doubledY := new(big.Int), new(big.Int)
	for _, placement := range placements {
		weight := big.NewInt(placement.instance.Item().Weight().Ticks())
		centreX := ints.AddExact(ints.MulExact(2, placement.position.x), placement.dimensions.length.Ticks())
		centreY := ints.AddExact(ints.MulExact(2, placement.position.y), placement.dimensions.width.Ticks())
		doubledX.Add(doubledX, new(big.Int).Mul(weight, big.NewInt(centreX)))
		doubledY.Add(doubledY, new(big.Int).Mul(weight, big.NewInt(centreY)))
	}
	return ints.Max(axisOffsetPPM(doubledX, totalWeight, inner.length.Ticks()),
		axisOffsetPPM(doubledY, totalWeight, inner.width.Ticks()))
}

func axisOffsetPPM(doubledWeighted, totalWeight *big.Int, extent int64) int64 {
	reference := new(big.Int).Mul(totalWeight, big.NewInt(extent))
	numerator := new(big.Int).Sub(doubledWeighted, reference)
	numerator.Abs(numerator).Mul(numerator, million).Quo(numerator, reference)
	if numerator.Cmp(maxInt64) > 0 {
		return 1<<63 - 1
	}
	return numerator.Int64()
}
