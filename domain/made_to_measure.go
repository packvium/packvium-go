package domain

import (
	"errors"
	"fmt"
	"math/big"
)

// Made-to-measure containers.
//
// A made-to-measure container is cut by an on-demand box machine. Its declared inner
// dimensions are the largest box the machine can cut; MadeToMeasure adds the smallest size,
// the cut step and the prices. A trial packs the round's items into up to four solving
// envelopes (SolvingEnvelopes) and keeps the first that places every item; the committed box
// is the achieved extent rounded up onto the machine grid (Fabricate). Everything is exact
// integer arithmetic, so every engine derives the same rungs, box and price.

// LengthTicksPerMetre is one metre in length ticks.
const LengthTicksPerMetre = 16_000_000

var squareMetreTicks2 = big.NewInt(LengthTicksPerMetre * LengthTicksPerMetre)

// rungFactors are each compact rung's side as a fraction of the integer cube root of the
// round's volume.
var rungFactors = [][2]int64{{5, 4}, {3, 2}, {2, 1}}

var axisNames = [3]string{"length", "width", "height"}

// MadeToMeasure is what the machine can cut beyond the declared maximum, per axis in ticks.
// A minimum equal to the maximum pins an axis. TarePerSquareMetre is weight ticks per square
// metre of inner surface; OuterAllowance is the board added to each inner side.
type MadeToMeasure struct {
	minimum, increment, outerAllowance [3]int64
	costPerSquareMetreMinor            int64
	tarePerSquareMetre                 int64
}

// NewMadeToMeasure validates a machine in the reference order.
func NewMadeToMeasure(minimum, increment [3]int64, costPerSquareMetreMinor, tarePerSquareMetre int64,
	outerAllowance [3]int64) (*MadeToMeasure, error) {
	switch {
	case minOf(minimum) <= 0:
		return nil, errors.New("made_to_measure minimum must be positive")
	case minOf(increment) <= 0:
		return nil, errors.New("made_to_measure increment must be positive")
	case costPerSquareMetreMinor < 0 || tarePerSquareMetre < 0:
		return nil, errors.New("made_to_measure prices cannot be negative")
	case minOf(outerAllowance) < 0:
		return nil, errors.New("made_to_measure outer_allowance cannot be negative")
	}
	return &MadeToMeasure{minimum, increment, outerAllowance, costPerSquareMetreMinor, tarePerSquareMetre}, nil
}

func minOf(values [3]int64) int64 {
	lowest := values[0]
	for _, value := range values[1:] {
		if value < lowest {
			lowest = value
		}
	}
	return lowest
}

func (m *MadeToMeasure) Minimum() [3]int64              { return m.minimum }
func (m *MadeToMeasure) Increment() [3]int64            { return m.increment }
func (m *MadeToMeasure) OuterAllowance() [3]int64       { return m.outerAllowance }
func (m *MadeToMeasure) CostPerSquareMetreMinor() int64 { return m.costPerSquareMetreMinor }
func (m *MadeToMeasure) TarePerSquareMetre() int64      { return m.tarePerSquareMetre }

// admits refuses a machine whose grid does not land on its own maximum, so every reachable
// size is unambiguous and rounding an extent inside the maximum up onto the grid can never
// pass it. O(1).
func (m *MadeToMeasure) admits(maximum [3]int64) error {
	for axis, name := range axisNames {
		if m.minimum[axis] > maximum[axis] {
			return fmt.Errorf("made_to_measure minimum %s exceeds inner_dimensions", name)
		}
		if (maximum[axis]-m.minimum[axis])%m.increment[axis] != 0 {
			return fmt.Errorf("made_to_measure increment %s does not divide inner_dimensions minus minimum", name)
		}
	}
	return nil
}

// grid is the smallest cuttable size on axis that is at least value, for 0 <= value <= the
// machine maximum.
func (m *MadeToMeasure) grid(axis int, value int64) int64 {
	minimum, step := m.minimum[axis], m.increment[axis]
	if value < minimum {
		value = minimum
	}
	return minimum + (value-minimum+step-1)/step*step
}

// SurfaceArea is the inner surface of a closed box, 2(LW + LH + WH), in square ticks.
func SurfaceArea(sides [3]int64) *big.Int {
	length, width, height := big.NewInt(sides[0]), big.NewInt(sides[1]), big.NewInt(sides[2])
	area := new(big.Int).Mul(length, width)
	area.Add(area, new(big.Int).Mul(length, height))
	area.Add(area, new(big.Int).Mul(width, height))
	return area.Lsh(area, 1)
}

// PerSquareMetre is ceil(rate * area / T^2) as rate * q + ceil(rate * r / T^2) with
// q, r = divmod(area, T^2), the reference's exact split.
func PerSquareMetre(rate int64, area *big.Int) *big.Int {
	whole, fraction := new(big.Int).QuoRem(area, squareMetreTicks2, new(big.Int))
	rated := big.NewInt(rate)
	part := new(big.Int).Mul(rated, fraction)
	part.Add(part, squareMetreTicks2).Sub(part, big.NewInt(1)).Quo(part, squareMetreTicks2)
	return part.Add(part, whole.Mul(whole, rated))
}

// BoxCostMinorFor is a box's price: the container's own fee plus its board at priceMinor per
// square metre, rounded up.
func BoxCostMinorFor(priceMinor, feeMinor int64, sides [3]int64) *big.Int {
	cost := PerSquareMetre(priceMinor, SurfaceArea(sides))
	return cost.Add(cost, big.NewInt(feeMinor))
}

// icbrtCeil is the smallest non-negative c with c^3 >= value: doubling, then bisection.
func icbrtCeil(value *big.Int) *big.Int {
	cube := func(c *big.Int) *big.Int { return new(big.Int).Exp(c, big.NewInt(3), nil) }
	high := big.NewInt(1)
	for cube(high).Cmp(value) < 0 {
		high.Lsh(high, 1)
	}
	low := new(big.Int)
	for low.Cmp(high) < 0 {
		middle := new(big.Int).Add(low, high)
		middle.Rsh(middle, 1)
		if cube(middle).Cmp(value) >= 0 {
			high = middle
		} else {
			low = middle.Add(middle, big.NewInt(1))
		}
	}
	return low
}

// SolvingEnvelopes are the rungs one trial tries, ascending on every axis, the machine maximum
// last. edges are each remaining instance's envelope edges (nominal plus twice the
// clearance). O(r) over the r instances plus the cube root; the list depends only on their
// multiset.
func (m *MadeToMeasure) SolvingEnvelopes(maximum [3]int64, edges [][3]int64) [][3]int64 {
	volume := new(big.Int)
	var floors [3]int64
	for _, instance := range edges {
		product := new(big.Int).Mul(big.NewInt(instance[0]), big.NewInt(instance[1]))
		volume.Add(volume, product.Mul(product, big.NewInt(instance[2])))
		ranked := descending(instance)
		for rank := range ranked {
			if ranked[rank] > floors[rank] {
				floors[rank] = ranked[rank]
			}
		}
	}
	side := icbrtCeil(volume)
	var rungs [][3]int64
	for _, factor := range rungFactors {
		scaled := new(big.Int).Mul(side, big.NewInt(factor[0]))
		scaled.Add(scaled, big.NewInt(factor[1]-1)).Quo(scaled, big.NewInt(factor[1]))
		var rung [3]int64
		for axis := range rung {
			rung[axis] = m.clamp(axis, scaled, floors[axis], maximum[axis])
		}
		product := new(big.Int).Mul(big.NewInt(rung[0]), big.NewInt(rung[1]))
		if product.Mul(product, big.NewInt(rung[2])).Cmp(volume) >= 0 && !containsRung(rungs, rung) {
			rungs = append(rungs, rung)
		}
	}
	if !containsRung(rungs, maximum) {
		rungs = append(rungs, maximum)
	}
	return rungs
}

// clamp is grid(max(scaled, floor)) capped at the maximum: it sizes a solving envelope, never
// a box. A value past the maximum grids past it, so the cap answers without the arithmetic.
func (m *MadeToMeasure) clamp(axis int, scaled *big.Int, floor, maximum int64) int64 {
	if !scaled.IsInt64() || scaled.Int64() > maximum || floor > maximum {
		return maximum
	}
	value := scaled.Int64()
	if floor > value {
		value = floor
	}
	if sized := m.grid(axis, value); sized < maximum {
		return sized
	}
	return maximum
}

func descending(values [3]int64) [3]int64 {
	sorted := values
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			if sorted[j] > sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

func containsRung(rungs [][3]int64, wanted [3]int64) bool {
	for _, rung := range rungs {
		if rung == wanted {
			return true
		}
	}
	return false
}

// InstanceEdges is each instance's envelope edges, the input SolvingEnvelopes sizes rungs from.
func InstanceEdges(items []ItemInstance, clearanceTicks int64) [][3]int64 {
	grow := 2 * clearanceTicks
	edges := make([][3]int64, len(items))
	for index, item := range items {
		size := item.Item().Dimensions()
		edges[index] = [3]int64{size.Length().Ticks() + grow, size.Width().Ticks() + grow, size.Height().Ticks() + grow}
	}
	return edges
}

// Triple is dimensions as their three tick counts.
func Triple(dimensions Dimensions) [3]int64 {
	return [3]int64{dimensions.Length().Ticks(), dimensions.Width().Ticks(), dimensions.Height().Ticks()}
}
