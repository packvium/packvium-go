package validation

import (
	"math/big"
	"sort"
)

// Boxes and convex hulls in ticks, written the slow, obvious way the reference writes them:
// a validator that took the engine's shortcuts would stop being a second opinion.

type vertex = [3]*big.Int

// cuboid is an axis-aligned box in ticks; its far corner and base area are computed once.
type cuboid struct {
	x, y, z, length, width, height *big.Int
	x2, y2, z2, baseArea           *big.Int
}

func newCuboid(x, y, z, length, width, height *big.Int) cuboid {
	return cuboid{x, y, z, length, width, height, add(x, length), add(y, width), add(z, height), mul(length, width)}
}

func (c cuboid) intersects(other cuboid) bool {
	return c.x.Cmp(other.x2) < 0 && other.x.Cmp(c.x2) < 0 && c.y.Cmp(other.y2) < 0 && other.y.Cmp(c.y2) < 0 &&
		c.z.Cmp(other.z2) < 0 && other.z.Cmp(c.z2) < 0
}

func (c cuboid) contains(other cuboid) bool {
	return c.x.Cmp(other.x) <= 0 && c.y.Cmp(other.y) <= 0 && c.z.Cmp(other.z) <= 0 &&
		other.x2.Cmp(c.x2) <= 0 && other.y2.Cmp(c.y2) <= 0 && other.z2.Cmp(c.z2) <= 0
}

func (c cuboid) overlapAreaXY(other cuboid) *big.Int {
	dx := maxInt(bi(0), sub(minInt(c.x2, other.x2), maxInt(c.x, other.x)))
	dy := maxInt(bi(0), sub(minInt(c.y2, other.y2), maxInt(c.y, other.y)))
	return mul(dx, dy)
}

// vertices are the eight corners, in the reference's order.
func (c cuboid) vertices() []vertex {
	var corners []vertex
	for _, farX := range []bool{false, true} {
		for _, farY := range []bool{false, true} {
			for _, farZ := range []bool{false, true} {
				corner := vertex{c.x, c.y, c.z}
				if farX {
					corner[0] = c.x2
				}
				if farY {
					corner[1] = c.y2
				}
				if farZ {
					corner[2] = c.z2
				}
				corners = append(corners, corner)
			}
		}
	}
	return corners
}

func difference(left, right vertex) vertex {
	return vertex{sub(left[0], right[0]), sub(left[1], right[1]), sub(left[2], right[2])}
}

func cross(left, right vertex) vertex {
	return vertex{
		sub(mul(left[1], right[2]), mul(left[2], right[1])),
		sub(mul(left[2], right[0]), mul(left[0], right[2])),
		sub(mul(left[0], right[1]), mul(left[1], right[0])),
	}
}

func dot(left, right vertex) *big.Int {
	return add(add(mul(left[0], right[0]), mul(left[1], right[1])), mul(left[2], right[2]))
}

func isZero(axis vertex) bool {
	return axis[0].Sign() == 0 && axis[1].Sign() == 0 && axis[2].Sign() == 0
}

// primitiveAxis is the axis reduced by its gcd and signed so its first non-zero component is
// positive.
func primitiveAxis(axis vertex) vertex {
	divisor := gcd(gcd(axis[0], axis[1]), axis[2])
	if divisor.Sign() == 0 {
		return vertex{bi(0), bi(0), bi(0)}
	}
	reduced := vertex{floorQuo(axis[0], divisor), floorQuo(axis[1], divisor), floorQuo(axis[2], divisor)}
	for _, value := range reduced {
		if value.Sign() != 0 {
			if value.Sign() < 0 {
				return vertex{neg(reduced[0]), neg(reduced[1]), neg(reduced[2])}
			}
			return reduced
		}
	}
	return vertex{bi(0), bi(0), bi(0)}
}

func axisKey(axis vertex) string {
	return axis[0].String() + "," + axis[1].String() + "," + axis[2].String()
}

func compareVertices(left, right vertex) int {
	for axis := 0; axis < 3; axis++ {
		if order := left[axis].Cmp(right[axis]); order != 0 {
			return order
		}
	}
	return 0
}

// rotateHull reorients a hull the way the rotation table reorients its box, flipping one axis
// on an odd permutation so it stays a proper rotation, translated so its bounds start at 0.
func rotateHull(vertices []vertex, axes [3]int) []vertex {
	inversions := 0
	for first := 0; first < 3; first++ {
		for second := first + 1; second < 3; second++ {
			if axes[first] > axes[second] {
				inversions++
			}
		}
	}
	if len(vertices) == 0 {
		panic(shapeRefusal("a hull without vertices"))
	}
	turned := make([]vertex, len(vertices))
	for index, point := range vertices {
		x := point[axes[0]]
		if inversions%2 == 1 {
			x = neg(x)
		}
		turned[index] = vertex{x, point[axes[1]], point[axes[2]]}
	}
	low := turned[0]
	for _, point := range turned {
		low = vertex{minInt(low[0], point[0]), minInt(low[1], point[1]), minInt(low[2], point[2])}
	}
	for index, point := range turned {
		turned[index] = difference(point, low)
	}
	return turned
}

// eachTriple calls visit with every 3-combination, in itertools.combinations order.
func eachTriple(points []vertex, visit func(first, second, third vertex)) {
	for first := 0; first < len(points); first++ {
		for second := first + 1; second < len(points); second++ {
			for third := second + 1; third < len(points); third++ {
				visit(points[first], points[second], points[third])
			}
		}
	}
}

func pairDirections(points []vertex) []vertex {
	var directions []vertex
	for first := 0; first < len(points); first++ {
		for second := first + 1; second < len(points); second++ {
			directions = append(directions, difference(points[second], points[first]))
		}
	}
	return directions
}

func extent(points []vertex, axis vertex) (*big.Int, *big.Int) {
	var low, high *big.Int
	for _, point := range points {
		value := dot(point, axis)
		if low == nil || value.Cmp(low) < 0 {
			low = value
		}
		if high == nil || value.Cmp(high) > 0 {
			high = value
		}
	}
	return low, high
}

// hullAxes is the complete separating-axis superset: face normals of each hull and the cross
// products of their edge directions.
func hullAxes(left, right []vertex) []vertex {
	seen := map[string]bool{}
	var axes []vertex
	keep := func(axis vertex) {
		axis = primitiveAxis(axis)
		if key := axisKey(axis); !seen[key] && !isZero(axis) {
			seen[key] = true
			axes = append(axes, axis)
		}
	}
	for _, hull := range [][]vertex{left, right} {
		eachTriple(hull, func(first, second, third vertex) {
			keep(cross(difference(second, first), difference(third, first)))
		})
	}
	rightDirections := pairDirections(right)
	for _, one := range pairDirections(left) {
		for _, other := range rightDirections {
			keep(cross(one, other))
		}
	}
	return axes
}

// solidsOverlap is whether two convex solids share interior volume; touching is contact.
func solidsOverlap(left, right []vertex) bool {
	for _, axis := range hullAxes(left, right) {
		leftLow, leftHigh := extent(left, axis)
		rightLow, rightHigh := extent(right, axis)
		if leftHigh.Cmp(rightLow) <= 0 || rightHigh.Cmp(leftLow) <= 0 {
			return false
		}
	}
	return true
}

// hullVolume is the exact volume of a convex hull, 6V = sum of a . (b x c) over its outward
// faces.
func hullVolume(vertices []vertex) *big.Int {
	seen := map[string]bool{}
	var normals []vertex
	eachTriple(vertices, func(first, second, third vertex) {
		axis := primitiveAxis(cross(difference(second, first), difference(third, first)))
		if key := axisKey(axis); !isZero(axis) && !seen[key] {
			seen[key] = true
			normals = append(normals, axis)
		}
	})
	// The reference walks its normals sorted; the sum does not depend on the order.
	sort.Slice(normals, func(left, right int) bool { return compareVertices(normals[left], normals[right]) < 0 })
	six := bi(0)
	for _, axis := range normals {
		for _, outward := range []vertex{axis, {neg(axis[0]), neg(axis[1]), neg(axis[2])}} {
			_, highest := extent(vertices, outward)
			var face []vertex
			for _, point := range vertices {
				if dot(point, outward).Cmp(highest) == 0 {
					face = append(face, point)
				}
			}
			if len(face) < 3 {
				continue
			}
			ordered := windFace(face, outward)
			for index := 1; index+1 < len(ordered); index++ {
				six = add(six, dot(ordered[0], cross(ordered[index], ordered[index+1])))
			}
		}
	}
	return floorQuo(abs(six), bi(6))
}

// windFace is one planar convex face's corners in cyclic order seen from outside, by
// gift-wrapping from the lexicographically smallest vertex; collinear candidates resolve to
// the farthest.
func windFace(face []vertex, outward vertex) []vertex {
	start := face[0]
	for _, point := range face {
		if compareVertices(point, start) < 0 {
			start = point
		}
	}
	ordered := []vertex{start}
	current := start
	for step := 0; step < len(face); step++ {
		var following *vertex
		for index := range face {
			candidate := face[index]
			if compareVertices(candidate, current) == 0 {
				continue
			}
			if following == nil {
				following = &face[index]
				continue
			}
			turn := dot(cross(difference(*following, current), difference(candidate, current)), outward)
			reach, held := difference(candidate, current), difference(*following, current)
			if turn.Sign() < 0 || (turn.Sign() == 0 && dot(reach, reach).Cmp(dot(held, held)) > 0) {
				following = &face[index]
			}
		}
		if following == nil || compareVertices(*following, start) == 0 {
			break
		}
		ordered = append(ordered, *following)
		current = *following
	}
	return ordered
}
