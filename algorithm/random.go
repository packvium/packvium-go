package algorithm

const (
	randomModulus    int64 = 2_147_483_648
	randomMultiplier int64 = 1_103_515_245
	randomIncrement  int64 = 12_345
	randomDrawLimit  int64 = 4_294_967_296
)

// DeterministicRandom is a seeded linear congruential generator, identical in every engine.
// Only the high bits are published: the low bits of a power-of-two modulus have tiny periods,
// and the shuffle asks for very small bounds. Draws outside the largest whole multiple of the
// bound are rejected, so the result carries no modulo bias.
type DeterministicRandom struct{ state int64 }

// NewDeterministicRandom seeds the generator. Go's % keeps the dividend's sign, as the
// reference's does.
func NewDeterministicRandom(seed int64) *DeterministicRandom {
	return &DeterministicRandom{state: seed % randomModulus}
}

func (r *DeterministicRandom) bits() int64 {
	// state stays below 2^31 in magnitude, so the product cannot leave int64.
	r.state = (randomMultiplier*r.state + randomIncrement) % randomModulus
	return r.state >> 15
}

// NextInt is a uniform draw from [0, upper).
func (r *DeterministicRandom) NextInt(upper int64) int64 {
	if upper <= 1 {
		return 0
	}
	bound := randomDrawLimit - (randomDrawLimit % upper)
	for {
		draw := r.bits()*65_536 + r.bits()
		if draw < bound {
			return draw % upper
		}
	}
}

// Permutation is a Fisher-Yates shuffle of 0..n-1 from the last index down. O(n).
func (r *DeterministicRandom) Permutation(n int) []int {
	out := make([]int, n)
	for index := range out {
		out[index] = index
	}
	for index := n - 1; index > 0; index-- {
		other := int(r.NextInt(int64(index) + 1))
		out[index], out[other] = out[other], out[index]
	}
	return out
}
