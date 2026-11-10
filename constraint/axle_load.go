package constraint

import (
	"math"
	"math/big"

	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/internal/ints"
)

// Two-axle weight distribution.
//
// Two-point beam statics: taking moments about each axle in turn gives an exact fraction for
// what the other axle carries, assuming only that the container behaves like a rigid beam on
// exactly two supports. Exact throughout, because weight ticks times length ticks routinely
// leaves int64 for a heavy, metre-scale load. Both loads are compared to their limits by
// cross-multiplying the same fraction, never rounded to an intermediate axle load. O(n) in
// the units.

// AxleReactions is the front and rear reaction, each Numerator / Denominator. A numerator
// may be negative: a load far behind the rear axle lifts the front one.
type AxleReactions struct {
	Denominator    *big.Int
	FrontNumerator *big.Int
	RearNumerator  *big.Int
}

// Reactions takes load units rather than placements, because a placement-time candidate has
// none yet. A unit's box is its envelope, whose centre coincides with the physical item's
// because clearance pads both sides of every axis equally. Tare contributes at the doubled
// x-coordinate tareDoubledCenterX, which keeps an odd half tick exact.
func Reactions(axles [2]domain.Axle, units []LoadUnit, tareWeightTicks, tareDoubledCenterX int64) AxleReactions {
	frontX, rearX := big.NewInt(axles[0].Position().Ticks()), big.NewInt(axles[1].Position().Ticks())
	twoTotal := new(big.Int).Lsh(totalWeight(units, tareWeightTicks), 1)
	weightedX := doubledWeightedX(units, tareWeightTicks, tareDoubledCenterX)
	denominator := new(big.Int).Lsh(new(big.Int).Sub(rearX, frontX), 1)
	front := new(big.Int).Mul(twoTotal, rearX)
	front.Sub(front, weightedX)
	rear := new(big.Int).Mul(twoTotal, frontX)
	rear.Sub(weightedX, rear)
	return AxleReactions{denominator, front, rear}
}

// AxleLoadExceeded is the first axle, front then rear, bearing more than its own limit.
func AxleLoadExceeded(axles [2]domain.Axle, units []LoadUnit, tareWeightTicks, tareDoubledCenterX int64) *Violation {
	reactions := Reactions(axles, units, tareWeightTicks, tareDoubledCenterX)
	if overLimit(axles[0], reactions.FrontNumerator, reactions.Denominator) {
		return &Violation{"axle_overloaded", "front"}
	}
	if overLimit(axles[1], reactions.RearNumerator, reactions.Denominator) {
		return &Violation{"axle_overloaded", "rear"}
	}
	return nil
}

func overLimit(axle domain.Axle, numerator, denominator *big.Int) bool {
	limit := axle.MaxLoad()
	if limit == nil {
		return false
	}
	return numerator.Cmp(new(big.Int).Mul(big.NewInt(limit.Ticks()), denominator)) > 0
}

// AxleBalancedOrigins is the tightest x-origins that seat an item exactly on either axle's
// limit: front first when it has one, then rear.
//
// Candidate generation only proposes positions flush against a wall or another box's corner,
// which is incomplete once axle limits are in play: sometimes the only feasible spot floats
// away from every wall to keep this item's own moment from tipping one axle over its limit.
// Solving the boundary equation of Reactions for this item's centre turns the two limits into
// two extra origins worth trying, each biased toward the safe side of its own limit. The
// caller still runs every candidate through the ordinary checks. An origin beyond int64 is
// clamped to it, which the caller's range check discards either way.
func AxleBalancedOrigins(axles [2]domain.Axle, otherUnits []LoadUnit, tareWeightTicks, tareDoubledCenterX,
	itemWeightTicks, itemLengthTicks int64) []int64 {
	if itemWeightTicks <= 0 {
		return nil
	}
	frontX, rearX := big.NewInt(axles[0].Position().Ticks()), big.NewInt(axles[1].Position().Ticks())
	denominator := new(big.Int).Lsh(new(big.Int).Sub(rearX, frontX), 1)
	otherX := doubledWeightedX(otherUnits, tareWeightTicks, tareDoubledCenterX)
	total := totalWeight(otherUnits, tareWeightTicks)
	twoTotal := new(big.Int).Lsh(total.Add(total, big.NewInt(itemWeightTicks)), 1)
	weight, length, two := big.NewInt(itemWeightTicks), big.NewInt(itemLengthTicks), big.NewInt(2)
	var origins []int64
	if limit := axles[0].MaxLoad(); limit != nil {
		// Smallest doubled centre for which the front numerator stays within its limit.
		required := new(big.Int).Mul(twoTotal, rearX)
		required.Sub(required, new(big.Int).Mul(big.NewInt(limit.Ticks()), denominator)).Sub(required, otherX)
		centre := ceilDiv(required, weight)
		origins = append(origins, saturated(ceilDiv(centre.Sub(centre, length), two)))
	}
	if limit := axles[1].MaxLoad(); limit != nil {
		// Largest doubled centre for which the rear numerator stays within its limit.
		allowed := new(big.Int).Mul(big.NewInt(limit.Ticks()), denominator)
		allowed.Add(allowed, new(big.Int).Mul(twoTotal, frontX)).Sub(allowed, otherX)
		centre := floorDivBig(allowed, weight)
		origins = append(origins, saturated(floorDivBig(centre.Sub(centre, length), two)))
	}
	return origins
}

func totalWeight(units []LoadUnit, tareWeightTicks int64) *big.Int {
	total := big.NewInt(tareWeightTicks)
	for _, unit := range units {
		total.Add(total, big.NewInt(unit.WeightTicks))
	}
	return total
}

func doubledWeightedX(units []LoadUnit, tareWeightTicks, tareDoubledCenterX int64) *big.Int {
	weighted := new(big.Int).Mul(big.NewInt(tareWeightTicks), big.NewInt(tareDoubledCenterX))
	term := new(big.Int)
	for _, unit := range units {
		centre := ints.AddExact(ints.MulExact(2, unit.Box.Origin().X()), unit.Box.Dimensions().Length().Ticks())
		weighted.Add(weighted, term.Mul(big.NewInt(unit.WeightTicks), big.NewInt(centre)))
	}
	return weighted
}

// floorDivBig is floor division by a strictly positive denominator.
func floorDivBig(numerator, denominator *big.Int) *big.Int {
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if numerator.Sign() < 0 && remainder.Sign() != 0 {
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient
}

func ceilDiv(numerator, denominator *big.Int) *big.Int {
	quotient := floorDivBig(new(big.Int).Neg(numerator), denominator)
	return quotient.Neg(quotient)
}

func saturated(value *big.Int) int64 {
	if value.IsInt64() {
		return value.Int64()
	}
	if value.Sign() < 0 {
		return math.MinInt64
	}
	return math.MaxInt64
}
