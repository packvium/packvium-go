// Package ints holds the int64 helpers the language only gained as builtins after the
// module's minimum Go version.
package ints

import "math"

// Min is the smaller of its arguments.
func Min(first int64, rest ...int64) int64 {
	for _, value := range rest {
		if value < first {
			first = value
		}
	}
	return first
}

// Max is the larger of its arguments.
func Max(first int64, rest ...int64) int64 {
	for _, value := range rest {
		if value > first {
			first = value
		}
	}
	return first
}

// Overflow is the panic value of an exact operation whose result leaves int64. Inputs that
// large are refused at the API boundary rather than wrapped silently.
type Overflow struct{ Operation string }

func (o Overflow) Error() string { return "integer overflow in " + o.Operation }

// MulExact is a*b, panicking with Overflow rather than wrapping.
func MulExact(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	product := a * b
	if product/b != a || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		panic(Overflow{"multiplication"})
	}
	return product
}

// AddExact is a+b, panicking with Overflow rather than wrapping.
func AddExact(a, b int64) int64 {
	sum := a + b
	if (a > 0 && b > 0 && sum < 0) || (a < 0 && b < 0 && sum >= 0) {
		panic(Overflow{"addition"})
	}
	return sum
}

// Abs is |a|.
func Abs(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}
