package algorithm

import (
	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/constraint"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/internal/ints"
)

// MaxMaximalSpaces caps the surviving spaces, the smallest dropped first. Adversarial mixes
// carve roughly linearly more spaces per placement once size variety defeats the dominance
// filter, and every extra one multiplies the choose scan and the O(new * s) dominance check.
const MaxMaximalSpaces = 256

// MaximalSpaceSolver keeps the maximal free spaces and evaluates candidates at each space's
// origin: asking the finder for its globally best point and testing whether it equals a space
// origin would reject almost every space, so the point set is passed explicitly.
type MaximalSpaceSolver struct {
	constraints []constraint.PlacementConstraint
	scorer      CandidateScorer
}

// NewMaximalSpaceSolver runs the built-in pipeline plus constraints, ranked by scorer.
func NewMaximalSpaceSolver(constraints []constraint.PlacementConstraint, scorer CandidateScorer) *MaximalSpaceSolver {
	return &MaximalSpaceSolver{append([]constraint.PlacementConstraint(nil), constraints...), scorer}
}

func (*MaximalSpaceSolver) Name() string           { return "maximal_spaces" }
func (*MaximalSpaceSolver) OrderInsensitive() bool { return false }

// PackOne places batches in order, each item at the first space origin that holds it.
func (s *MaximalSpaceSolver) PackOne(container *domain.Container, sequence int64, items []domain.ItemInstance,
	cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) SingleContainerSolution {
	active := defaultConstraints(cfg.MinimumSupportRatio(), s.constraints)
	state := NewContainerState(container, sequence)
	var obstacleBoxes []domain.Box
	for _, obstacle := range container.Obstacles() {
		obstacleBoxes = append(obstacleBoxes, obstacle.Boxes()...)
	}
	spaces := SubtractAll([]Space{{domain.Point{}, container.InnerDimensions()}}, obstacleBoxes, stats)
	var unplaced []domain.ItemInstance
	batches := Batches(items)
	for position, batch := range batches {
		snapshot, saved, placed := state.Copy(), spaces, true
		if untilTimeLimit(func() {
			for _, item := range batch {
				stats.SearchNodesExpanded++
				chosen := s.choose(state, spaces, item, cfg, active, stats, deadline)
				if chosen == nil {
					placed = false
					return
				}
				state = Extended(state, item, *chosen)
				spaces = SubtractAll(spaces, []domain.Box{domain.NewBox(chosen.Point, chosen.EnvelopeDimensions)}, stats)
			}
		}) {
			pending := concatInstances(unplaced, Flatten(batches[position:]))
			return SingleContainerSolution{State: snapshot, Unpacked: pending, TimeLimitReached: true}
		}
		if !placed {
			state, spaces = snapshot, saved
			unplaced = append(unplaced, batch...)
		}
	}
	return plainSolution(state, unplaced)
}

func (s *MaximalSpaceSolver) choose(state *ContainerState, spaces []Space, item domain.ItemInstance,
	cfg *config.PackingConfig, active []constraint.PlacementConstraint, stats *SearchStats,
	deadline *Deadline) *Candidate {
	for _, space := range spaces {
		deadline.Check()
		for _, candidate := range FindCandidates(state, item, cfg, active, stats, deadline, s.scorer, unlimited,
			[]domain.Point{space.Origin}) {
			if candidate.EnvelopeDimensions.FitsInside(space.Dimensions) {
				chosen := candidate
				return &chosen
			}
		}
	}
	return nil
}

// subtract is the up-to-six maximal slabs of the space that survive carving the box out of
// it.
func subtract(space Space, box domain.Box) []Space {
	source := space.box()
	if !source.Intersects(box) {
		return []Space{space}
	}
	o, b := source.Origin(), box.Origin()
	out := make([]Space, 0, 6)
	out = pushSpace(out, o.X(), o.Y(), o.Z(), ints.Min(source.X2(), b.X()), source.Y2(), source.Z2())
	out = pushSpace(out, ints.Max(o.X(), box.X2()), o.Y(), o.Z(), source.X2(), source.Y2(), source.Z2())
	out = pushSpace(out, o.X(), o.Y(), o.Z(), source.X2(), ints.Min(source.Y2(), b.Y()), source.Z2())
	out = pushSpace(out, o.X(), ints.Max(o.Y(), box.Y2()), o.Z(), source.X2(), source.Y2(), source.Z2())
	out = pushSpace(out, o.X(), o.Y(), o.Z(), source.X2(), source.Y2(), ints.Min(source.Z2(), b.Z()))
	return pushSpace(out, o.X(), o.Y(), ints.Max(o.Z(), box.Z2()), source.X2(), source.Y2(), source.Z2())
}

func pushSpace(out []Space, x1, y1, z1, x2, y2, z2 int64) []Space {
	if x2 > x1 && y2 > y1 && z2 > z1 {
		return append(out, Space{domain.MustPoint(x1, y1, z1), domain.MustDimensions(x2-x1, y2-y1, z2-z1)})
	}
	return out
}

// carved is a space and whether a carve produced it, which is all the dominance check needs.
type carved struct {
	space Space
	isNew bool
}

// SubtractAll carves every box out of spaces. Precondition: spaces is containment-free (a
// single whole space, or this function's own output). An untouched survivor can then neither
// contain nor be contained by another, so only new slabs need the dominance check: O(new * s)
// instead of O(s^2). stats may be nil.
func SubtractAll(spaces []Space, boxes []domain.Box, stats *SearchStats) []Space {
	working := make([]carved, len(spaces))
	for index, space := range spaces {
		working[index] = carved{space, false}
	}
	for _, box := range boxes {
		if stats != nil {
			stats.SpacePartitions += int64(len(working))
		}
		next := make([]carved, 0, len(working)+6)
		for _, entry := range working {
			if !entry.space.box().Intersects(box) {
				next = append(next, entry)
				continue
			}
			for _, part := range subtract(entry.space, box) {
				next = append(next, carved{part, true})
			}
		}
		working = next
	}
	// A repeated space keeps its first position and its last flag, as an insertion-ordered
	// map overwrite does.
	position := make(map[Space]int, len(working))
	var unique []carved
	for _, entry := range working {
		if at, seen := position[entry.space]; seen {
			unique[at] = entry
			continue
		}
		position[entry.space] = len(unique)
		unique = append(unique, entry)
	}
	order := stableOrder(len(unique), func(index int) SortKey {
		origin := unique[index].space.Origin
		return SortKey{Int(origin.Z()), Int(origin.Y()), Int(origin.X()),
			Big(negated(unique[index].space.Dimensions.Volume()))}
	})
	kept := make([]carved, len(order))
	for at, index := range order {
		kept[at] = unique[index]
	}
	// Without this filter every placement multiplies the list into overlapping duplicates.
	var result []Space
	for index, entry := range kept {
		if entry.isNew && dominated(kept, index) {
			continue
		}
		result = append(result, entry.space)
	}
	if len(result) <= MaxMaximalSpaces {
		return result
	}
	byVolume := stableOrder(len(result), func(index int) SortKey {
		return SortKey{Big(negated(result[index].Dimensions.Volume()))}
	})
	largest := make([]Space, MaxMaximalSpaces)
	for at := range largest {
		largest[at] = result[byVolume[at]]
	}
	byOrigin := stableOrder(len(largest), func(index int) SortKey {
		origin := largest[index].Origin
		return SortKey{Int(origin.Z()), Int(origin.Y()), Int(origin.X())}
	})
	out := make([]Space, len(largest))
	for at, index := range byOrigin {
		out[at] = largest[index]
	}
	return out
}

func dominated(kept []carved, index int) bool {
	mine := kept[index].space.box()
	for other := range kept {
		if other != index && kept[other].space.box().Contains(mine) {
			return true
		}
	}
	return false
}
