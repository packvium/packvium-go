package domain

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/toxakara/packvium-go/compression"
)

// IsValidNesting reports whether a and b are exactly the nested-column relationship
// nesting_height describes: the same item type, the same footprint, one sunk into the other
// by precisely the declared nesting height.
func IsValidNesting(a, b Placement) bool {
	if a.instance.item.ID() != b.instance.item.ID() || a.instance.item.spec.NestingHeight == nil {
		return false
	}
	nesting := a.instance.item.spec.NestingHeight.Ticks()
	boxA, boxB := a.EnvelopeBox(), b.EnvelopeBox()
	if boxA.origin.x != boxB.origin.x || boxA.origin.y != boxB.origin.y || boxA.X2() != boxB.X2() || boxA.Y2() != boxB.Y2() {
		return false
	}
	low, high := boxA, boxB
	if boxA.origin.z > boxB.origin.z {
		low, high = boxB, boxA
	}
	if low.origin.z == high.origin.z {
		return false
	}
	return low.Z2()-high.origin.z == nesting
}

// nestingOverlapVolume is the double-counted volume between adjacent nested layers. Grouped
// by (item, footprint) and checked only against the immediate z-neighbour: O(n log n).
func nestingOverlapVolume(placements []Placement) *big.Int {
	groups := map[string][]Placement{}
	var order []string
	for _, placement := range placements {
		if placement.instance.item.spec.NestingHeight == nil {
			continue
		}
		box := placement.EnvelopeBox()
		key := fmt.Sprintf("%s|%d,%d,%d,%d", placement.instance.item.ID(), box.origin.x, box.origin.y, box.X2(), box.Y2())
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], placement)
	}
	overlap := new(big.Int)
	for _, key := range order {
		group := groups[key]
		if len(group) < 2 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].envelopeOrigin.z < group[j].envelopeOrigin.z })
		for index := 0; index+1 < len(group); index++ {
			lower := group[index]
			if IsValidNesting(lower, group[index+1]) {
				slice := new(big.Int).Mul(big.NewInt(lower.instance.item.spec.NestingHeight.Ticks()),
					big.NewInt(lower.envelopeDimensions.BaseArea()))
				overlap.Add(overlap, slice)
			}
		}
	}
	return overlap
}

// OccupiedVolume is the space one placement actually takes: its hull for a convex_hull item,
// its compressed height for a compressible one, otherwise its box.
func OccupiedVolume(placement Placement) *big.Int {
	item := placement.instance.item
	if item.spec.ShapeType == ConvexHull {
		shape, err := item.HullShape(placement.rotation)
		if err != nil {
			panic(err)
		}
		return shape.Volume().Big()
	}
	if item.spec.MaxCompressionPressureKPA == nil {
		return placement.dimensions.Volume()
	}
	limit := *item.spec.MaxCompressionPressureKPA
	dimensions := placement.dimensions
	footprint := dimensions.BaseArea()
	pressure, err := compression.AppliedPressure(placement.topLoad, big.NewInt(footprint))
	if err != nil {
		panic(err)
	}
	// A crushed item has no meaningful occupied volume, and the arrangement is already
	// invalid; reporting the uncompressed figure keeps that a reported issue.
	if pressure.ExceedsKPA(limit) {
		return dimensions.Volume()
	}
	ratio := int64(0)
	if item.spec.CompressionRatioPPM != nil {
		ratio = *item.spec.CompressionRatioPPM
	}
	height, err := compression.EffectiveHeightTicks(dimensions.height.Ticks(), ratio, limit, pressure)
	if err != nil {
		panic(err)
	}
	return new(big.Int).Mul(big.NewInt(footprint), big.NewInt(height))
}

// UsedVolume is the physical volume occupied by placements, nesting overlap removed.
// O(n log n).
func UsedVolume(placements []Placement) *big.Int {
	total := new(big.Int)
	for _, placement := range placements {
		total.Add(total, OccupiedVolume(placement))
	}
	return total.Sub(total, nestingOverlapVolume(placements))
}

// UsedVolumeDelta is the exact volume added by appending placement: O(1) without nesting,
// one O(n) scan with it.
func UsedVolumeDelta(placements []Placement, placement Placement) *big.Int {
	if placement.instance.item.spec.NestingHeight == nil {
		return OccupiedVolume(placement)
	}
	nesting := placement.instance.item.spec.NestingHeight.Ticks()
	overlap := new(big.Int)
	for _, existing := range placements {
		if IsValidNesting(existing, placement) {
			overlap.Add(overlap, new(big.Int).Mul(big.NewInt(nesting), big.NewInt(placement.envelopeDimensions.BaseArea())))
		}
	}
	return new(big.Int).Sub(placement.dimensions.Volume(), overlap)
}
