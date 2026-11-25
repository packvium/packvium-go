package domain

// The flat-bag girth rule.
//
// A flat_bag container is two sheets of usable flat size L x W filled to at most thickness H,
// its inner dimensions. With a, b and c the largest envelope x2, y2 and z2 over its
// placements, measured from the container origin, a packing is admitted only when the
// ordinary boundary holds and a + c <= L and b + c <= W: a loop of film round the package's
// cross-section is at least as long as that section's perimeter. The extents only grow as
// boxes are added, so a verdict reached for a candidate never flips when more items follow.

// FlatBag is the one deformable boundary kind.
const FlatBag = "flat_bag"

// DeformableBoundaryKinds are the kinds a container may declare.
var DeformableBoundaryKinds = []string{FlatBag}

// GirthAdmits reports whether a package of extents (a, b, c) passes both girth inequalities.
// The rigid boundary is the caller's own check. O(1); a, b and c lie inside the bag, so the
// sums cannot overflow.
func GirthAdmits(inner Dimensions, a, b, c int64) bool {
	return a+c <= inner.Length().Ticks() && b+c <= inner.Width().Ticks()
}

// FitsFlatBagAlone reports whether one box of these dimensions, alone at the origin, fits a
// flat bag.
func FitsFlatBagAlone(inner, dimensions Dimensions) bool {
	return dimensions.FitsInside(inner) &&
		GirthAdmits(inner, dimensions.Length().Ticks(), dimensions.Width().Ticks(), dimensions.Height().Ticks())
}

// Extent is (max x2, max y2, max z2) over the envelopes of placements, zero for none. O(p).
func Extent(placements []Placement) [3]int64 {
	var extent [3]int64
	for _, placement := range placements {
		box := placement.EnvelopeBox()
		if box.X2() > extent[0] {
			extent[0] = box.X2()
		}
		if box.Y2() > extent[1] {
			extent[1] = box.Y2()
		}
		if box.Z2() > extent[2] {
			extent[2] = box.Z2()
		}
	}
	return extent
}
