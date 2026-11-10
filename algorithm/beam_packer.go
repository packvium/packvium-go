package algorithm

import (
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
	incumbentKey := nodeKey(incumbent, nil)
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
			candidate := beamNode{expansion.state, concatInstances(expansion.unplaced, future)}
			if candidateKey := nodeKey(candidate, nil); candidateKey.Compare(incumbentKey) < 0 {
				incumbent, incumbentKey = candidate, candidateKey
			}
		}
		if len(expansions) == 0 {
			break
		}
		beam = firstNodes(sortNodes(expansions, future), width)
		if exhausted {
			return SingleContainerSolution{State: incumbent.state, Unpacked: incumbent.unplaced,
				TimeLimitReached: deadline.Expired()}
		}
	}
	ordered := sortNodes(beam, nil)
	chosen := incumbent
	if len(ordered) > 0 && nodeKey(ordered[0], nil).Compare(incumbentKey) < 0 {
		chosen = ordered[0]
	}
	return plainSolution(chosen.state, chosen.unplaced)
}

func sortNodes(nodes []beamNode, future []domain.ItemInstance) []beamNode {
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
		beam = firstNodes(sortNodes(expansions, nil), int64(width))
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

// maximumCountByVolume is how many of the cheapest future items could still fit by volume
// alone. O(f log f).
func maximumCountByVolume(future []domain.ItemInstance, capacity *big.Int) int64 {
	costs := make([]*big.Int, len(future))
	for index, item := range future {
		costs[index] = item.Item().Dimensions().Volume()
	}
	sort.Slice(costs, func(i, j int) bool { return costs[i].Cmp(costs[j]) < 0 })
	used, count := new(big.Int), int64(0)
	for _, cost := range costs {
		next := new(big.Int).Add(used, cost)
		if next.Cmp(capacity) > 0 {
			break
		}
		used, count = next, count+1
	}
	return count
}

func unpackedLowerBound(state *ContainerState, unplaced, future []domain.ItemInstance) int64 {
	if len(future) == 0 {
		return int64(len(unplaced))
	}
	possible := int64(len(future))
	nesting := false
	for _, item := range future {
		nesting = nesting || item.Item().NestingHeight() != nil
	}
	if !nesting {
		capacity := new(big.Int).Sub(state.Container.InnerDimensions().Volume(), state.UsedVolume)
		possible = ints.Min(possible, maximumCountByVolume(future, capacity))
	}
	if payload := state.Container.MaxPayload(); payload != nil {
		capacity := ints.Max(0, payload.Ticks()-state.PayloadTicks)
		weights := make([]int64, len(future))
		for index, item := range future {
			weights[index] = item.Item().Weight().Ticks()
		}
		sort.Slice(weights, func(i, j int) bool { return weights[i] < weights[j] })
		used, count := int64(0), int64(0)
		for _, weight := range weights {
			if used+weight > capacity {
				break
			}
			used += weight
			count++
		}
		possible = ints.Min(possible, count)
	}
	return int64(len(unplaced)) + int64(len(future)) - possible
}

func nodeKey(node beamNode, future []domain.ItemInstance) SortKey {
	state := node.state
	return SortKey{Int(unpackedLowerBound(state, node.unplaced, future)), Int(int64(len(node.unplaced))),
		Int(-int64(len(state.Placements))), Int(state.MaxZ), Big(negated(state.UsedVolume)),
		Str(state.PlacementSignature())}
}
