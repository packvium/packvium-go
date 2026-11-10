package algorithm

import (
	"time"

	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/internal/ints"
)

// MinimumSliceNS is the least a multi-start is ever given, or a long start list would hand
// out slices too small to place one item.
const MinimumSliceNS int64 = 1_000_000

// Clock reads monotonic nanoseconds from an arbitrary origin.
type Clock func() int64

// MonotonicClock reads the process's monotonic clock; it is the only clock the engine reads.
func MonotonicClock() Clock {
	origin := time.Now()
	return func() int64 { return int64(time.Since(origin)) }
}

// TimeLimitReached unwinds a search that ran out of time or effort. It travels as a panic
// value, exactly where the reference throws, and every solver that can stop early recovers it
// at the boundary the reference catches it at.
type TimeLimitReached struct{ message string }

func (t TimeLimitReached) Error() string { return t.message }

// Deadline is a monotonic budget that can hand bounded sub-budgets to individual starts. An
// attached effort budget is read against a live SearchStats, so Expired trips on whichever
// bound, time or counted work, is hit first.
type Deadline struct {
	limitNS int64
	clock   Clock
	started int64
	effort  *config.EffortBudget
	stats   *SearchStats
}

func newDeadline(limitNS int64, clock Clock, effort *config.EffortBudget, stats *SearchStats) *Deadline {
	return &Deadline{limitNS: limitNS, clock: clock, started: clock(), effort: effort, stats: stats}
}

// DeadlineOfMilliseconds is a budget on the monotonic clock.
func DeadlineOfMilliseconds(milliseconds int64) *Deadline {
	return DeadlineWithClock(milliseconds, MonotonicClock())
}

// DeadlineWithClock is a budget on the given clock.
func DeadlineWithClock(milliseconds int64, clock Clock) *Deadline {
	return newDeadline(ints.MulExact(milliseconds, 1_000_000), clock, nil, nil)
}

// ElapsedMS is the whole milliseconds since the budget started.
func (d *Deadline) ElapsedMS() int64 { return (d.clock() - d.started) / 1_000_000 }

// RemainingNS is the budget left, negative once it is spent.
func (d *Deadline) RemainingNS() int64 { return d.limitNS - (d.clock() - d.started) }

// EffortExceeded reports whether the attached effort budget is spent.
func (d *Deadline) EffortExceeded() bool {
	return d.effort != nil && d.stats != nil &&
		d.effort.Exceeded(d.stats.CandidatesEvaluated, d.stats.PlacementsAttempted, d.stats.SearchNodesExpanded)
}

// Expired reports whether either bound has been hit.
func (d *Deadline) Expired() bool { return d.EffortExceeded() || d.RemainingNS() <= 0 }

// Slice is this budget shared among parts starts, never below MinimumSliceNS unless less
// than that remains.
func (d *Deadline) Slice(parts int64) *Deadline {
	remaining := d.RemainingNS()
	if parts <= 1 {
		return newDeadline(remaining, d.clock, d.effort, d.stats)
	}
	return newDeadline(ints.Max(remaining/parts, ints.Min(remaining, MinimumSliceNS)), d.clock, d.effort, d.stats)
}

// WithEffort binds an effort budget to one start's own stats, keeping the same time bound.
func (d *Deadline) WithEffort(budget *config.EffortBudget, stats *SearchStats) *Deadline {
	return newDeadline(d.RemainingNS(), d.clock, budget, stats)
}

// Check unwinds the search with TimeLimitReached once the budget is spent.
func (d *Deadline) Check() {
	if d.Expired() {
		panic(TimeLimitReached{"packing time limit reached"})
	}
}

// untilTimeLimit runs search and reports whether it was cut by TimeLimitReached. Any other
// panic keeps unwinding.
func untilTimeLimit(search func()) (reached bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(TimeLimitReached); !ok {
				panic(recovered)
			}
			reached = true
		}
	}()
	search()
	return false
}
