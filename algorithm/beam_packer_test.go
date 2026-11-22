package algorithm

import (
	"math"
	"math/big"
	"testing"
)

// maximumCardinality is the most costs any subset can hold within capacity, by brute force.
func maximumCardinality(costs []int64, capacity int64) int64 {
	best := int64(0)
	for mask := 0; mask < 1<<uint(len(costs)); mask++ {
		total, count := int64(0), int64(0)
		for index, cost := range costs {
			if mask&(1<<uint(index)) != 0 {
				total += cost
				count++
			}
		}
		if total <= capacity && count > best {
			best = count
		}
	}
	return best
}

// The reference's test_additive_cardinality_bound_is_an_admissible_relaxation: cheapest first
// over the prefix sums is exactly the largest count the additive resource admits.
func TestTheAdditiveCardinalityBoundIsAnAdmissibleRelaxation(t *testing.T) {
	for _, costs := range [][]int64{{2, 3, 5}, {1, 4, 4, 9}, {3, 3, 3, 7, 8}, {0, 0, 2, 6}} {
		total := int64(0)
		for _, cost := range costs {
			total += cost
		}
		weights := cumulativeWeights(append([]int64(nil), costs...))
		volumes := make([]*big.Int, len(costs))
		running := new(big.Int)
		for index, cost := range costs {
			running = new(big.Int).Add(running, big.NewInt(cost))
			volumes[index] = running
		}
		for capacity := int64(0); capacity <= total; capacity++ {
			exact := maximumCardinality(costs, capacity)
			if got := countWithinWeight(weights, capacity); got != exact {
				t.Fatalf("%v within %d: weight bound %d, exact %d", costs, capacity, got, exact)
			}
			if got := countWithinVolume(volumes, big.NewInt(capacity)); got != exact {
				t.Fatalf("%v within %d: volume bound %d, exact %d", costs, capacity, got, exact)
			}
		}
	}
}

func TestPrefixWeightsPastTheInt64RangeNeverCount(t *testing.T) {
	weights := cumulativeWeights([]int64{1, math.MaxInt64 - 1, math.MaxInt64})
	if len(weights) != 2 || countWithinWeight(weights, math.MaxInt64) != 2 {
		t.Fatalf("prefix sums %v", weights)
	}
}
