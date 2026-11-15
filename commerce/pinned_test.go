package commerce

import (
	"math/big"
	"testing"
)

// A pinned version is found by its position; numbers outside 1..count, including ones past
// int64, are not found rather than wrapped onto a real version.
func TestPinnedPositionIsTheNumberMinusOneWithinTheHistory(t *testing.T) {
	beyond := new(big.Int).Lsh(big.NewInt(1), 64)
	cases := []struct {
		number   *big.Int
		count    int
		position int
		found    bool
	}{
		{big.NewInt(1), 3, 0, true},
		{big.NewInt(3), 3, 2, true},
		{big.NewInt(0), 3, 0, false},
		{big.NewInt(-1), 3, 0, false},
		{big.NewInt(4), 3, 0, false},
		{big.NewInt(1), 0, 0, false},
		{beyond, 3, 0, false},
		{new(big.Int).Add(beyond, big.NewInt(1)), 3, 0, false},
	}
	for _, testCase := range cases {
		position, found := pinnedPosition(testCase.number, testCase.count)
		if position != testCase.position || found != testCase.found {
			t.Errorf("pinnedPosition(%s, %d) = %d, %v", testCase.number, testCase.count, position, found)
		}
	}
}
