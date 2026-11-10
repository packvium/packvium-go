// Package sequence holds the loading and unloading dependency graphs over one container's
// placements, the safe orders derived from them, and an independent replay that checks any
// order step by step.
//
// Both graphs share one accessibility primitive. A direction names which container wall's
// region a box's sweep occupies, not a direction of travel: the region between a box and its
// +x wall is the same whether the box leaves or arrives through it. The two graphs differ
// only in which items count as present at each step and which structural dependency gates a
// step at all.
package sequence

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/toxakara/packvium-go/domain"
)

// InvalidDirectionError is a direction outside the six-value vocabulary.
type InvalidDirectionError = domain.InvalidDirectionError

// SequenceStuckCode is the discriminator a SequenceError carries on the wire.
const SequenceStuckCode = "sequence_stuck"

// SequenceReplayCode is the discriminator a SequenceReplayError carries on the wire.
const SequenceReplayCode = "sequence_replay"

// SequenceError is that no safe removal order exists: every remaining placement is blocked
// in every allowed direction. It carries every index still stuck, not only the first, since
// a real deadlock is usually a mutual one.
type SequenceError struct{ stuck []int }

func newSequenceError(stuck []int) *SequenceError { return &SequenceError{sortedCopy(stuck)} }

// Stuck is the indices still unremovable, ascending.
func (e *SequenceError) Stuck() []int { return append([]int(nil), e.stuck...) }

func (e *SequenceError) Error() string {
	return "no safe order exists: placements " + listRepr(e.stuck) + " are mutually blocking"
}

// ToMap is the wire form.
func (e *SequenceError) ToMap() map[string]interface{} {
	return map[string]interface{}{"code": SequenceStuckCode, "stuck": wireIndexes(e.stuck)}
}

// RouteSequenceError is that no safe order fully empties a stop before the route moves on.
// Distinct from SequenceError: that one means no unconstrained order exists at all; this
// means the stop-by-stop order the route demands does not.
type RouteSequenceError struct {
	stop  int64
	stuck []int
}

// Stop is the first stop that cannot be fully unloaded.
func (e *RouteSequenceError) Stop() int64 { return e.stop }

// Stuck is the placements still due there, ascending.
func (e *RouteSequenceError) Stuck() []int { return append([]int(nil), e.stuck...) }

func (e *RouteSequenceError) Error() string {
	return fmt.Sprintf("stop %d: placements %s cannot be unloaded there", e.stop, listRepr(e.stuck))
}

// SequenceReplayError is a step of a given order that is not feasible against only the
// placements present at that point. Distinct from SequenceError: the order supplied is
// wrong, whether or not another order would work. Index and step are -1 when the order is
// not a permutation at all.
type SequenceReplayError struct {
	index, step int64
	reason      string
}

func replayError(index, step int, reason string) *SequenceReplayError {
	return &SequenceReplayError{int64(index), int64(step), reason}
}

func (e *SequenceReplayError) Index() int64   { return e.index }
func (e *SequenceReplayError) Step() int64    { return e.step }
func (e *SequenceReplayError) Reason() string { return e.reason }

func (e *SequenceReplayError) Error() string {
	return fmt.Sprintf("step %d: placement %d is not safe there (%s)", e.step, e.index, e.reason)
}

// ToMap is the wire form.
func (e *SequenceReplayError) ToMap() map[string]interface{} {
	return map[string]interface{}{"code": SequenceReplayCode, "index": e.index, "step": e.step, "reason": e.reason}
}

func sortedCopy(indexes []int) []int {
	sorted := append([]int(nil), indexes...)
	sort.Ints(sorted)
	return sorted
}

// listRepr is a sorted index list as the reference prints one: [0, 1, 2].
func listRepr(sorted []int) string {
	parts := make([]string, len(sorted))
	for position, index := range sorted {
		parts[position] = strconv.Itoa(index)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// wireIndexes is a sorted index list in the form the JSON encoder takes.
func wireIndexes(sorted []int) []interface{} {
	out := make([]interface{}, len(sorted))
	for position, index := range sorted {
		out[position] = int64(index)
	}
	return out
}
