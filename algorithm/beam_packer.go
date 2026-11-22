package algorithm

import (
	"math"
	"math/big"
	"sort"

	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/ints"
)

// beamNode is a partial packing and the items it has given up on.
type beamNode struct {
	state    *ContainerState
	unplaced []domain.ItemInstance
}

// BeamPack is greedy placement widened into a beam by maxCandidatesPerItem. A width of one is
// plain best-fit greedy; wider keeps that many partial packings alive so an early
// locally-best corner cannot dictate the whole layout. O(batches * width * branch * F) for F
// the cost of one FindCandidates, plus O(width * m log width) for the node keys.
func BeamPack(container *domain.Container, sequence int64, items []domain.ItemInstance, cfg *config.PackingConfig,
	constraints []constraint.PlacementConstraint, stats *SearchStats, deadline *Deadline,
	scorer CandidateScorer) SingleContainerSolution {
	branchWidth := int(ints.Max(1, cfg.MaxCandidatesPerItem()))
	width := ints.Max(1, cfg.ContainerPlanBeamWidth())
	if width == 1 {
		return packLegacy(container, sequence, items, cfg, constraints, stats, deadline, scorer, branchWidth)
	}
	nodeLimit, nodes := cfg.ContainerPlanNodeLimit(), int64(0)
	batches := Batches(items)
	beam := []beamNode{{NewContainerState(container, sequence), nil}}
	incumbent := greedyRollout(container, sequence, batches, cfg, constraints, stats, deadline, scorer)
	// nodeKey builds an O(m) placement signature and a knapsack bound; computing it once per
	// node instead of once per comparison changes no ordering.
	incumbentKey := nodeKey(incumbent, futureCosts{})
	incumbentUnplaced := int64(len(incumbent.unplaced))
	for position, batch := range batches {
		var expansions []beamNode
		exhausted := false
		for _, node := range beam {
			if nodes >= nodeLimit {
				exhausted = true
				break
			}
			nodes++
			stats.SearchNodesExpanded++
			var children []*ContainerState
			if untilTimeLimit(func() {
				children = placeBatch(node.state, batch, cfg, constraints, stats, deadline, scorer, branchWidth)
			}) {
				exhausted, children = true, nil
			}
			for _, child := range children {
				expansions = append(expansions, beamNode{child, node.unplaced})
			}
			expansions = append(expansions, beamNode{node.state, concatInstances(node.unplaced, batch)})
		}
		future := Flatten(batches[position+1:])
		for _, expansion := range expansions {
			// The key of the expansion with every later batch left unplaced, built without the
			// concatenation unless it wins.
			unplaced := int64(len(expansion.unplaced) + len(future))
			// More unplaced items loses on the key's first component whatever follows, so the
			// signature -- the costly O(m) part -- is built only for a candidate that can win.
			if unplaced > incumbentUnplaced {
				continue
			}
			candidateKey := stateKey(expansion.state, unplaced, unplaced)
			if candidateKey.Compare(incumbentKey) < 0 {
				incumbent = beamNode{expansion.state, concatInstances(expansion.unplaced, future)}
				incumbentKey, incumbentUnplaced = candidateKey, unplaced
			}
		}
		if len(expansions) == 0 {
			break
		}
		beam = firstNodes(sortNodes(expansions, newFutureCosts(future, container.MaxPayload() != nil)), width)
		if exhausted {
			return SingleContainerSolution{State: incumbent.state, Unpacked: incumbent.unplaced,
				TimeLimitReached: deadline.Expired()}
		}
	}
	ordered := sortNodes(beam, futureCosts{})
	chosen := incumbent
	if len(ordered) > 0 && nodeKey(ordered[0], futureCosts{}).Compare(incumbentKey) < 0 {
		chosen = ordered[0]
	}
	return plainSolution(chosen.state, chosen.unplaced)
}

func sortNodes(nodes []beamNode, future futureCosts) []beamNode {
	order := stableOrder(len(nodes), func(index int) SortKey { return nodeKey(nodes[index], future) })
	out := make([]beamNode, len(nodes))
	for position, index := range order {
		out[position] = nodes[index]
	}
	return out
}

func firstNodes(nodes []beamNode, count int64) []beamNode {
	if int64(len(nodes)) > count {
		return nodes[:count]
	}
	return nodes
}

// greedyRollout is the anytime seed: one greedy pass, so a complete incumbent exists before
// the beam starts.
func greedyRollout(container *domain.Container, sequence int64, batches [][]domain.ItemInstance,
	cfg *config.PackingConfig, constraints []constraint.PlacementConstraint, stats *SearchStats, deadline *Deadline,
	scorer CandidateScorer) beamNode {
	greedy := NewContainerState(container, sequence)
	var unplaced []domain.ItemInstance
	for position, batch := range batches {
		var children []*ContainerState
		if untilTimeLimit(func() {
			children = placeBatch(greedy, batch, cfg, constraints, stats, deadline, scorer, 1)
		}) {
			unplaced = append(unplaced, Flatten(batches[position:])...)
			break
		}
		if len(children) == 0 {
			unplaced = append(unplaced, batch...)
		} else {
			greedy = children[0]
		}
	}
	return beamNode{greedy, unplaced}
}

// packLegacy is the historical fast/balanced candidate beam. The quality-only anytime seed
// would only double work at plan width one, so the low-latency path stays separate.
func packLegacy(container *domain.Container, sequence int64, items []domain.ItemInstance, cfg *config.PackingConfig,
	constraints []constraint.PlacementConstraint, stats *SearchStats, deadline *Deadline, scorer CandidateScorer,
	width int) SingleContainerSolution {
	batches := Batches(items)
	beam := []beamNode{{NewContainerState(container, sequence), nil}}
	for position, batch := range batches {
		var expansions []beamNode
		exhausted := false
		for _, node := range beam {
			stats.SearchNodesExpanded++
			var children []*ContainerState
			if untilTimeLimit(func() {
				children = placeBatch(node.state, batch, cfg, constraints, stats, deadline, scorer, width)
			}) {
				exhausted, children = true, nil
			}
			if len(children) == 0 {
				expansions = append(expansions, beamNode{node.state, concatInstances(node.unplaced, batch)})
			}
			for _, child := range children {
				expansions = append(expansions, beamNode{child, node.unplaced})
			}
		}
		beam = firstNodes(sortNodes(expansions, futureCosts{}), int64(width))
		if exhausted {
			best := beam[0]
			pending := Flatten(batches[position+1:])
			return SingleContainerSolution{State: best.state, Unpacked: concatInstances(best.unplaced, pending),
				TimeLimitReached: true}
		}
	}
	best := beam[0]
	return plainSolution(best.state, best.unplaced)
}

// PlaceBatch is the states reachable by placing every member of the batch, at most width
// (unlimited for every one), best first; empty when the batch cannot fit. A multi-item batch
// is placed greedily, one best candidate per member.
func PlaceBatch(state *ContainerState, batch []domain.ItemInstance, cfg *config.PackingConfig,
	constraints []constraint.PlacementConstraint, stats *SearchStats, deadline *Deadline, scorer CandidateScorer,
	width int) []*ContainerState {
	return placeBatch(state, batch, cfg, constraints, stats, deadline, scorer, width)
}

func placeBatch(state *ContainerState, batch []domain.ItemInstance, cfg *config.PackingConfig,
	constraints []constraint.PlacementConstraint, stats *SearchStats, deadline *Deadline, scorer CandidateScorer,
	width int) []*ContainerState {
	if len(batch) == 1 {
		candidates := FindCandidates(state, batch[0], cfg, constraints, stats, deadline, scorer, width, nil)
		out := make([]*ContainerState, len(candidates))
		for index, candidate := range candidates {
			out[index] = Extended(state, batch[0], candidate)
		}
		return out
	}
	working := state
	for _, item := range batch {
		candidates := FindCandidates(working, item, cfg, constraints, stats, deadline, scorer, 1, nil)
		if len(candidates) == 0 {
			return nil
		}
		working = Extended(working, item, candidates[0])
	}
	return []*ContainerState{working}
}

// Extended is a fork of state with item placed at candidate.
func Extended(state *ContainerState, item domain.ItemInstance, candidate Candidate) *ContainerState {
	child := state.Copy()
	child.Add(domain.NewPlacement(item, candidate.Position, candidate.Rotation, candidate.Dimensions, candidate.Point,
		candidate.EnvelopeDimensions, SupportRatio(state, item, candidate)))
	return child
}

// futureCosts are the prefix sums of the later batches' ascending volumes and weights, built
// once per beam level rather than once per node: O(f log f) per level instead of O(w f log f).
// A nil list means that bound does not apply (a nesting item, or no payload limit).
type futureCosts struct {
	volumes []*big.Int
	weights []int64
	count   int64
}

func newFutureCosts(future []domain.ItemInstance, payloadLimited bool) futureCosts {
	if len(future) == 0 {
		return futureCosts{}
	}
	costs := futureCosts{count: int64(len(future))}
	nesting := false
	for _, item := range future {
		nesting = nesting || item.Item().NestingHeight() != nil
	}
	if !nesting {
		volumes := make([]*big.Int, len(future))
		for index, item := range future {
			volumes[index] = item.Item().Dimensions().Volume()
		}
		sort.Slice(volumes, func(i, j int) bool { return volumes[i].Cmp(volumes[j]) < 0 })
		for index := 1; index < len(volumes); index++ {
			volumes[index] = new(big.Int).Add(volumes[index-1], volumes[index])
		}
		costs.volumes = volumes
	}
	if payloadLimited {
		weights := make([]int64, len(future))
		for index, item := range future {
			weights[index] = item.Item().Weight().Ticks()
		}
		sort.Slice(weights, func(i, j int) bool { return weights[i] < weights[j] })
		costs.weights = cumulativeWeights(weights)
	}
	return costs
}

// countWithinVolume is how many of the cheapest future items could still fit by volume alone.
// Choosing the cheapest first can only overestimate how many fit, so the bound stays
// admissible; costs are non-negative, so the prefix sums never decrease and the count is one
// binary search: O(log f) per node instead of O(f), with no allocation.
func countWithinVolume(cumulative []*big.Int, capacity *big.Int) int64 {
	return int64(sort.Search(len(cumulative), func(index int) bool { return cumulative[index].Cmp(capacity) > 0 }))
}

// cumulativeWeights is the prefix sums of ascending weights, cut before the first sum past
// the int64 range: no capacity is that large, so no count can reach those entries anyway.
func cumulativeWeights(ascending []int64) []int64 {
	for index := 1; index < len(ascending); index++ {
		if ascending[index] > math.MaxInt64-ascending[index-1] {
			return ascending[:index]
		}
		ascending[index] += ascending[index-1]
	}
	return ascending
}

func countWithinWeight(cumulative []int64, capacity int64) int64 {
	return int64(sort.Search(len(cumulative), func(index int) bool { return cumulative[index] > capacity }))
}

// unpackedLowerBound is an admissible volume and weight bound on the final unpacked count.
func unpackedLowerBound(state *ContainerState, unplaced int64, future futureCosts) int64 {
	if future.count == 0 {
		return unplaced
	}
	possible := future.count
	if future.volumes != nil {
		capacity := new(big.Int).Sub(state.Container.InnerDimensions().Volume(), state.UsedVolume)
		possible = ints.Min(possible, countWithinVolume(future.volumes, capacity))
	}
	if future.weights != nil {
		capacity := ints.Max(0, state.Container.MaxPayload().Ticks()-state.PayloadTicks)
		possible = ints.Min(possible, countWithinWeight(future.weights, capacity))
	}
	return unplaced + future.count - possible
}

func nodeKey(node beamNode, future futureCosts) SortKey {
	unplaced := int64(len(node.unplaced))
	return stateKey(node.state, unpackedLowerBound(node.state, unplaced, future), unplaced)
}

func stateKey(state *ContainerState, bound, unplaced int64) SortKey {
	return SortKey{Int(bound), Int(unplaced), Int(-int64(len(state.Placements))), Int(state.MaxZ),
		Big(negated(state.UsedVolume)), Str(state.PlacementSignature())}
}
