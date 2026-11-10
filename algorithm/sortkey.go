// Package algorithm holds the solver layer: the single-container solvers, the container
// planner, the portfolio orchestrator, the objective scorers and the extension points a caller
// can plug in. Everything mutable here lives for one solve only.
package algorithm

import (
	"math"
	"math/big"
	"sort"
)

type keyKind uint8

const (
	kindInt keyKind = iota
	kindFloat
	kindBig
	kindString
	kindBool
	kindKey
)

// KeyPart is one element of a SortKey: an exact integer, a double, an unbounded integer, a
// string, a boolean or a nested key.
type KeyPart struct {
	kind   keyKind
	whole  int64
	double float64
	huge   *big.Int
	text   string
	nested SortKey
}

// SortKey is a precomputed ordering key with the reference engine's tuple semantics.
// Elements compare left to right and a key that is a prefix of another is smaller. Numbers
// compare by exact value whatever their representation, strings by code point (which Go's
// byte order on UTF-8 is), booleans false before true.
type SortKey []KeyPart

// Int is an exact integer key element.
func Int(value int64) KeyPart { return KeyPart{kind: kindInt, whole: value} }

// Float is a double key element; an infinity sorts past every finite value.
func Float(value float64) KeyPart { return KeyPart{kind: kindFloat, double: value} }

// Big is an unbounded integer key element. The value is not copied, so callers pass one they
// will not modify.
func Big(value *big.Int) KeyPart { return KeyPart{kind: kindBig, huge: value} }

// Str is a string key element.
func Str(value string) KeyPart { return KeyPart{kind: kindString, text: value} }

// Bool is a boolean key element.
func Bool(value bool) KeyPart {
	part := KeyPart{kind: kindBool}
	if value {
		part.whole = 1
	}
	return part
}

// Nested embeds one key in another.
func Nested(key SortKey) KeyPart { return KeyPart{kind: kindKey, nested: key} }

// Key builds a SortKey from its elements.
func Key(parts ...KeyPart) SortKey { return SortKey(parts) }

// Ints is a key of exact integers.
func Ints(values []int64) SortKey {
	key := make(SortKey, len(values))
	for index, value := range values {
		key[index] = Int(value)
	}
	return key
}

// Compare is negative, zero or positive as k sorts before, with or after other.
func (k SortKey) Compare(other SortKey) int {
	shared := len(k)
	if len(other) < shared {
		shared = len(other)
	}
	for index := 0; index < shared; index++ {
		if order := compareParts(k[index], other[index]); order != 0 {
			return order
		}
	}
	return compareInts(int64(len(k)), int64(len(other)))
}

// Less reports whether k sorts strictly before other.
func (k SortKey) Less(other SortKey) bool { return k.Compare(other) < 0 }

func compareParts(left, right KeyPart) int {
	switch {
	case left.kind == kindInt && right.kind == kindInt:
		return compareInts(left.whole, right.whole)
	case isNumber(left.kind) && isNumber(right.kind):
		return compareNumbers(left, right)
	case left.kind == kindString && right.kind == kindString:
		return compareStrings(left.text, right.text)
	case left.kind == kindBool && right.kind == kindBool:
		return compareInts(left.whole, right.whole)
	case left.kind == kindKey && right.kind == kindKey:
		return left.nested.Compare(right.nested)
	}
	// Mixing element types at one position is an engine bug, never a caller input.
	panic("algorithm: sort key elements of different types cannot be ordered")
}

func isNumber(kind keyKind) bool { return kind == kindInt || kind == kindFloat || kind == kindBig }

func compareInts(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	}
	return 0
}

// compareNumbers is exact across representations: a double and an integer are compared by
// value, never by rounding the integer to a double.
func compareNumbers(left, right KeyPart) int {
	leftInfinity, rightInfinity := infinitySign(left), infinitySign(right)
	if leftInfinity != 0 || rightInfinity != 0 {
		return compareInts(int64(leftInfinity), int64(rightInfinity))
	}
	if left.kind == kindFloat && right.kind == kindFloat {
		switch {
		case left.double < right.double:
			return -1
		case left.double > right.double:
			return 1
		}
		return 0
	}
	if left.kind != kindFloat && right.kind != kindFloat {
		return exactInteger(left).Cmp(exactInteger(right))
	}
	return exactFloat(left).Cmp(exactFloat(right))
}

func infinitySign(part KeyPart) int {
	if part.kind != kindFloat {
		return 0
	}
	if math.IsNaN(part.double) {
		panic("algorithm: NaN has no order")
	}
	if math.IsInf(part.double, 1) {
		return 1
	}
	if math.IsInf(part.double, -1) {
		return -1
	}
	return 0
}

func exactInteger(part KeyPart) *big.Int {
	if part.kind == kindBig {
		return part.huge
	}
	return big.NewInt(part.whole)
}

func exactFloat(part KeyPart) *big.Float {
	switch part.kind {
	case kindFloat:
		return new(big.Float).SetFloat64(part.double)
	case kindBig:
		return new(big.Float).SetInt(part.huge)
	}
	return new(big.Float).SetInt64(part.whole)
}

// stableOrder is the permutation that sorts n elements by their keys, equal keys keeping
// their input order, as Python's sorted(key=...) does. Each key is built once: O(n) keys and
// O(n log n) comparisons.
func stableOrder(n int, key func(index int) SortKey) []int {
	keys := make([]SortKey, n)
	order := make([]int, n)
	for index := 0; index < n; index++ {
		keys[index] = key(index)
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool { return keys[order[i]].Compare(keys[order[j]]) < 0 })
	return order
}
