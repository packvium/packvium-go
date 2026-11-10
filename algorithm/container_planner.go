package algorithm

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// containerPlanner spreads one start's items across containers: greedily, one container per
// round, or as a beam over whole container plans when containerPlanBeamWidth exceeds one.
type containerPlanner struct {
	selector ContainerSelector
	reasons  *unpackedReasons
}

// planOutcome is what one start produced across every container it opened.
type planOutcome struct {
	packed          []*domain.PackedContainer
	unpacked        []domain.UnpackedItem
	proven          bool
	reached         bool
	dominantLattice bool
}

// orderedContainers tries containers cheapest first, then smallest, then by id.
func orderedContainers(containers []*domain.Container) []*domain.Container {
	order := stableOrder(len(containers), func(index int) SortKey {
		container := containers[index]
		return SortKey{Int(container.CostMinor()), Big(container.InnerDimensions().Volume()), Str(container.ID())}
	})
	out := make([]*domain.Container, len(containers))
	for position, index := range order {
		out[position] = containers[index]
	}
	return out
}

func maximumContainers(containers []*domain.Container, items []domain.ItemInstance, cfg *config.PackingConfig) int64 {
	if limit := cfg.MaxContainers(); limit != nil {
		return *limit
	}
	total := int64(0)
	for _, container := range containers {
		if quantity := container.Quantity(); quantity != nil {
			total += *quantity
		} else {
			total += int64(len(items))
		}
	}
	return total
}

// inventory is the containers left per type, in request order; a nil count is unlimited.
type inventory struct {
	ids  []string
	left map[string]*int64
}

func newInventory(containers []*domain.Container) inventory {
	stock := inventory{left: map[string]*int64{}}
	for _, container := range containers {
		stock.ids = append(stock.ids, container.ID())
		stock.left[container.ID()] = container.Quantity()
	}
	return stock
}

func (i inventory) exhausted(id string) bool {
	left := i.left[id]
	return left != nil && *left <= 0
}

// taken is a copy with one container of id used.
func (i inventory) taken(id string) inventory {
	copied := inventory{ids: i.ids, left: make(map[string]*int64, len(i.left))}
	for key, value := range i.left {
		copied.left[key] = value
	}
	if left := i.left[id]; left != nil {
		decremented := *left - 1
		copied.left[id] = &decremented
	}
	return copied
}

// signature renders the counts in request order; the keys are the same for every plan, so
// the values alone identify an inventory.
func (i inventory) signature() string {
	parts := make([]string, len(i.ids))
	for index, id := range i.ids {
		if left := i.left[id]; left != nil {
			parts[index] = strconv.FormatInt(*left, 10)
		} else {
			parts[index] = "null"
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type trial struct {
	score    SortKey
	solution SingleContainerSolution
}

// greedy runs rounds: each round packs every container type and commits the best one.
// O(rounds * c * S).
func (p *containerPlanner) greedy(solver SingleContainerSolver, items []domain.ItemInstance,
	containers []*domain.Container, cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) planOutcome {
	remaining := items
	var packed []*domain.PackedContainer
	reached, exhaustive, dominantLattice := false, true, true
	stock := newInventory(containers)
	sequence := map[string]int64{}
	maximum := maximumContainers(containers, items, cfg)
	ordered := orderedContainers(containers)
	for len(remaining) > 0 && int64(len(packed)) < maximum {
		if deadline.Expired() {
			reached = true
			break
		}
		var trials []trial
		for _, container := range ordered {
			if stock.exhausted(container.ID()) {
				continue
			}
			var one SingleContainerSolution
			if untilTimeLimit(func() {
				one = solver.PackOne(container, sequence[container.ID()]+1, remaining, cfg, stats, deadline)
			}) {
				reached = true
				break
			}
			if one.TimeLimitReached {
				reached = true
			}
			if one.State.PlacementCount() == 0 {
				if reached {
					break
				}
				continue
			}
			trials = append(trials, trial{p.roundScore(container, one, len(remaining), cfg), one})
			if reached {
				break
			}
		}
		if len(trials) == 0 {
			break
		}
		best := trials[stableOrder(len(trials), func(index int) SortKey { return trials[index].score })[0]].solution
		chosen := best.State.Container
		sequence[chosen.ID()]++
		exhaustive = exhaustive && best.Exhaustive
		dominantLattice = dominantLattice && best.DominantLattice
		stock = stock.taken(chosen.ID())
		committed, ids := commitState(best.State)
		packed = append(packed, committed)
		remaining = withoutIDs(remaining, ids)
		if reached {
			break
		}
	}
	unpacked := p.explainAll(remaining, containers, reached, packed, cfg)
	// Optimality may be claimed only when an exhaustive search filled a single container, the
	// cheapest, since containers are tried in cost order.
	proven := exhaustive && !reached && len(unpacked) == 0 && len(packed) == 1
	return planOutcome{packed, unpacked, proven, reached, dominantLattice}
}

func withoutIDs(items []domain.ItemInstance, ids map[string]bool) []domain.ItemInstance {
	var kept []domain.ItemInstance
	for _, item := range items {
		if !ids[item.ID()] {
			kept = append(kept, item)
		}
	}
	return kept
}

func (p *containerPlanner) roundScore(container *domain.Container, one SingleContainerSolution, remainingCount int,
	cfg *config.PackingConfig) SortKey {
	billed := cfg.Objective() == "shipping_cost" || cfg.Objective() == LandedCostObjective
	if !billed || cfg.DimensionalWeightDivisor() == nil {
		return p.selector.Score(container, one)
	}
	billedTicks := BilledTicksForConfig(container, one.State.PayloadTicks, cfg)
	placed := one.State.PlacementCount()
	if cfg.Objective() == "shipping_cost" {
		return SortKey{Int(-placed), Int(billedTicks), Str(container.ID())}
	}
	// Landed cost ranks the round by the money the finished score will charge: this trial is
	// committed verbatim, so its billed weight is final. An unpriceable trial is unshippable,
	// not merely dear, so it sorts behind every priceable one.
	charge := ChargeOrUnpriceable(container, billedTicks)
	perContainer := ints.Max(placed, 1)
	return SortKey{Int(charge), Int((int64(remainingCount) + perContainer - 1) / perContainer), Int(-placed),
		Str(container.ID())}
}

// commitState freezes a finished state into a packed container, loads assigned, and the ids
// it consumed.
func commitState(state *ContainerState) (*domain.PackedContainer, map[string]bool) {
	ids := map[string]bool{}
	if state.LatticeSummary != nil {
		for _, item := range state.LatticeItems {
			ids[item.ID()] = true
		}
		return domain.NewLatticePackedContainer(state.Container, state.Sequence, state.LatticeSummary,
			state.LatticeItems), ids
	}
	for _, placement := range state.Placements {
		ids[placement.Instance().ID()] = true
	}
	return domain.NewPackedContainer(state.Container, state.Sequence, AssignTopLoads(state.Placements)), ids
}

func (p *containerPlanner) explainAll(remaining []domain.ItemInstance, containers []*domain.Container, reached bool,
	packed []*domain.PackedContainer, cfg *config.PackingConfig) []domain.UnpackedItem {
	unpacked := make([]domain.UnpackedItem, 0, len(remaining))
	for _, item := range remaining {
		unpacked = append(unpacked, p.reasons.explain(item, containers, reached, packed, cfg))
	}
	return unpacked
}

// plan is a partial container plan in the beam.
type plan struct {
	packed     []*domain.PackedContainer
	remaining  []domain.ItemInstance
	stock      inventory
	sequence   int64
	exhaustive bool
	dominant   bool
}

// plans is a beam over whole container plans. Each level expands every plan by every
// container type, keeps the best per (remaining items, inventory) signature, and ranks by an
// admissible bound. O(levels * width * c * S) with the node limit as a hard cap.
func (p *containerPlanner) plans(solver SingleContainerSolver, items []domain.ItemInstance,
	containers []*domain.Container, cfg *config.PackingConfig, stats *SearchStats, deadline *Deadline) planOutcome {
	initial := plan{nil, items, newInventory(containers), 0, true, true}
	beam := []plan{initial}
	incumbent := initial
	nodes, reached := int64(0), false
	maximum := maximumContainers(containers, items, cfg)
	ordered := orderedContainers(containers)
	for len(beam) > 0 && nodes < cfg.ContainerPlanNodeLimit() {
		var expansions []plan
		for _, current := range beam {
			if len(current.remaining) == 0 || int64(len(current.packed)) >= maximum {
				if betterPlan(current, incumbent, cfg) {
					incumbent = current
				}
				continue
			}
			for _, container := range ordered {
				if nodes >= cfg.ContainerPlanNodeLimit() {
					break
				}
				if deadline.Expired() {
					reached = true
					break
				}
				if current.stock.exhausted(container.ID()) {
					continue
				}
				nodes++
				var one SingleContainerSolution
				if untilTimeLimit(func() {
					one = solver.PackOne(container, current.sequence+1, current.remaining, cfg, stats, deadline)
				}) {
					reached = true
					break
				}
				reached = reached || one.TimeLimitReached
				if one.State.PlacementCount() == 0 {
					continue
				}
				child := extendPlan(current, container, one)
				expansions = append(expansions, child)
				if betterPlan(child, incumbent, cfg) {
					incumbent = child
				}
				if reached {
					break
				}
			}
			if reached {
				break
			}
		}
		if reached || len(expansions) == 0 {
			break
		}
		beam = nextBeam(expansions, containers, cfg)
	}
	unpacked := p.explainAll(incumbent.remaining, containers, reached, incumbent.packed, cfg)
	proven := incumbent.exhaustive && !reached && len(unpacked) == 0 && len(incumbent.packed) == 1
	return planOutcome{incumbent.packed, unpacked, proven, reached, incumbent.dominant}
}

func extendPlan(current plan, container *domain.Container, one SingleContainerSolution) plan {
	committed, ids := commitState(one.State)
	packed := append(append([]*domain.PackedContainer(nil), current.packed...), committed)
	return plan{packed, withoutIDs(current.remaining, ids), current.stock.taken(container.ID()), current.sequence + 1,
		current.exhaustive && one.Exhaustive, current.dominant && one.DominantLattice}
}

func nextBeam(expansions []plan, containers []*domain.Container, cfg *config.PackingConfig) []plan {
	position := map[string]int{}
	var dominant []plan
	for _, candidate := range expansions {
		signature := instanceSignature(candidate.remaining) + "::" + candidate.stock.signature()
		at, seen := position[signature]
		if !seen {
			position[signature] = len(dominant)
			dominant = append(dominant, candidate)
			continue
		}
		if planScore(candidate, nil, cfg).Compare(planScore(dominant[at], nil, cfg)) < 0 {
			dominant[at] = candidate
		}
	}
	order := stableOrder(len(dominant), func(index int) SortKey { return planBound(dominant[index], containers, cfg) })
	width := int(ints.Min(cfg.ContainerPlanBeamWidth(), int64(len(order))))
	ranked := make([]plan, width)
	for at := 0; at < width; at++ {
		ranked[at] = dominant[order[at]]
	}
	return ranked
}

func betterPlan(candidate, incumbent plan, cfg *config.PackingConfig) bool {
	return planScore(candidate, candidate.remaining, cfg).Compare(planScore(incumbent, incumbent.remaining, cfg)) < 0
}

func planScore(current plan, remaining []domain.ItemInstance, cfg *config.PackingConfig) ObjectiveScore {
	unpacked := make([]domain.UnpackedItem, len(remaining))
	for index, item := range remaining {
		unpacked[index] = domain.NewUnpackedItem(item, "search_exhausted", nil)
	}
	raw := RawSolution{SolverName: "container_plan", Containers: current.packed, Unpacked: unpacked,
		Stats: &SearchStats{}}
	return ResolveObjective(cfg.Objective(), cfg).Score(raw)
}

// planBound is a lower bound on any completion of the plan, then the remaining signature as
// tie-break.
func planBound(current plan, containers []*domain.Container, cfg *config.PackingConfig) SortKey {
	score := append(ObjectiveScore(nil), planScore(current, nil, cfg)...)
	index := 2
	if cfg.Objective() == "default" {
		index = 1
	}
	score[index] += additionalContainerLowerBound(current, containers)
	return append(Ints(score), Str(instanceSignature(current.remaining)))
}

func additionalContainerLowerBound(current plan, containers []*domain.Container) int64 {
	var available []*domain.Container
	for _, container := range containers {
		if !current.stock.exhausted(container.ID()) {
			available = append(available, container)
		}
	}
	if len(current.remaining) == 0 || len(available) == 0 {
		return 0
	}
	lower := int64(0)
	nesting := false
	for _, item := range current.remaining {
		nesting = nesting || item.Item().NestingHeight() != nil
	}
	if !nesting {
		capacity := new(big.Int)
		for _, container := range available {
			if volume := container.InnerDimensions().Volume(); volume.Cmp(capacity) > 0 {
				capacity = volume
			}
		}
		if capacity.Sign() > 0 {
			required := new(big.Int)
			for _, item := range current.remaining {
				required.Add(required, item.Item().Dimensions().Volume())
			}
			required.Add(required, capacity).Sub(required, big.NewInt(1)).Quo(required, capacity)
			lower = ints.Max(lower, exactInt64(required))
		}
	}
	allLimited := true
	capacity := int64(0)
	for _, container := range available {
		payload := container.MaxPayload()
		if payload == nil {
			allLimited = false
			break
		}
		capacity = ints.Max(capacity, payload.Ticks())
	}
	if allLimited && capacity > 0 {
		required := int64(0)
		for _, item := range current.remaining {
			required += item.Item().Weight().Ticks()
		}
		lower = ints.Max(lower, (required+capacity-1)/capacity)
	}
	return lower
}

func instanceSignature(items []domain.ItemInstance) string { return orderSignature(items) }
