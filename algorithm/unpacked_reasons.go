package algorithm

import (
	"github.com/packvium/packvium-go/config"
	"github.com/packvium/packvium-go/constraint"
	"github.com/packvium/packvium-go/domain"
)

// unpackedReasons says why an item ended up unpacked, proven where pure geometry can prove
// it.
type unpackedReasons struct {
	customConstraints []constraint.PlacementConstraint
	scorer            CandidateScorer
}

type reason struct {
	code    string
	details []string
}

func (r *unpackedReasons) explain(item domain.ItemInstance, containers []*domain.Container, reached bool,
	packed []*domain.PackedContainer, opened map[string]int64, cfg *config.PackingConfig) domain.UnpackedItem {
	found := r.reason(item, containers, reached)
	code := found.code
	if code == "no_feasible_placement" && r.supportIsTheBlocker(item, packed, cfg) {
		code = "insufficient_support"
	}
	if code == "no_feasible_placement" {
		if limit := limitReason(item, containers, opened, cfg); limit != "" {
			code = limit
		}
	}
	return domain.NewUnpackedItem(item, code, found.details)
}

// openedByType counts the containers of each type a layout opened. O(P), once per layout.
func openedByType(packed []*domain.PackedContainer) map[string]int64 {
	opened := map[string]int64{}
	for _, container := range packed {
		opened[container.Container().ID()]++
	}
	return opened
}

// limitReason is the container limit that left an item out, for the no_feasible_placement
// fallback, or "" when no limit explains it. Only containers that admit the item
// structurally (a fitting rotation, its weight, its eligibility tags) are asked. When an axle
// refuses the item alone in every one of them, the axle is the reason; otherwise, when every
// one that would take it alone is out of stock, or max_containers is reached, the inventory
// is. max_items, cumulative payload, tag limits, stop accessibility and an axle's share of a
// full load can only refuse an item a fresh container would take, so they surface here too.
// O(C * R) for C container types and R rotations; it reads only the request and the layout.
func limitReason(instance domain.ItemInstance, containers []*domain.Container, opened map[string]int64,
	cfg *config.PackingConfig) string {
	item := instance.Item()
	var envelopes []domain.Dimensions
	for _, oriented := range item.Dimensions().UniqueRotations(item.AllowedRotations()) {
		envelopes = append(envelopes, oriented.Dimensions.Expand(cfg.Clearance()))
	}
	weight := item.Weight().Ticks()
	homes := item.EligibleContainerTags()
	var admitting []*domain.Container
	compatible := 0
	for _, container := range containers {
		payload := container.MaxPayload()
		if !anyFitsAlone(container, envelopes) || (payload != nil && weight > payload.Ticks()) ||
			(homes.Len() > 0 && !homes.Intersects(container.Tags())) {
			continue
		}
		compatible++
		if axlesAdmitAlone(container, envelopes, weight) {
			admitting = append(admitting, container)
		}
	}
	if compatible == 0 {
		return ""
	}
	if len(admitting) == 0 {
		return "axle_overloaded"
	}
	if limit := cfg.MaxContainers(); limit != nil {
		total := int64(0)
		for _, count := range opened {
			total += count
		}
		if total >= *limit {
			return "container_inventory_exhausted"
		}
	}
	for _, container := range admitting {
		if quantity := container.Quantity(); quantity == nil || opened[container.ID()] < *quantity {
			return ""
		}
	}
	return "container_inventory_exhausted"
}

func anyFitsAlone(container *domain.Container, envelopes []domain.Dimensions) bool {
	for _, envelope := range envelopes {
		if fitsAlone(container, envelope) {
			return true
		}
	}
	return false
}

func axlesAdmitAlone(container *domain.Container, envelopes []domain.Dimensions, weight int64) bool {
	axles := container.Axles()
	if axles == nil {
		return true
	}
	for _, envelope := range envelopes {
		if fitsAlone(container, envelope) && constraint.AxleAdmitsAlone(*axles, container.TareWeight().Ticks(),
			container.InnerDimensions().Length().Ticks(), weight, envelope.Length().Ticks()) {
			return true
		}
	}
	return false
}

// fitsAlone reports whether one box of these dimensions fits the container empty: the frame,
// and for a flat bag the girth rule too, so a bag too thin for an item proves it unplaceable.
func fitsAlone(container *domain.Container, dimensions domain.Dimensions) bool {
	if container.IsFlatBag() {
		return domain.FitsFlatBagAlone(container.InnerDimensions(), dimensions)
	}
	return dimensions.FitsInside(container.InnerDimensions())
}

// reason runs the structural proofs first, since they hold whatever the search did, even past
// the deadline; then what a registered constraint can prove; then what the search observed.
// O(c * r).
func (r *unpackedReasons) reason(item domain.ItemInstance, containers []*domain.Container, reached bool) reason {
	if !fitsSomewhere(item, containers, domain.AllRotations()) {
		return reason{"no_compatible_container_dimensions", nil}
	}
	// Distinguishes "too big in any rotation" from "only this rotation restriction rules every
	// container out"; both are pure geometry.
	if !fitsSomewhere(item, containers, item.Item().AllowedRotations()) {
		return reason{"rotation_restricted", nil}
	}
	weightFits := false
	for _, container := range containers {
		payload := container.MaxPayload()
		weightFits = weightFits || payload == nil || item.Item().Weight().Ticks() <= payload.Ticks()
	}
	if !weightFits {
		return reason{"payload_exceeded", nil}
	}
	homes := item.Item().EligibleContainerTags()
	eligible := homes.Len() == 0
	for _, container := range containers {
		eligible = eligible || homes.Intersects(container.Tags())
	}
	if !eligible {
		return reason{"no_eligible_container", nil}
	}
	// Ranked after the geometric proofs: an impossible request is impossible whatever a policy
	// says, and naming the policy first would send a caller to fix the wrong thing.
	if proven := r.provenByConstraint(item, containers); proven != nil {
		return *proven
	}
	if reached {
		return reason{"time_limit", nil}
	}
	if item.Item().Group() != nil {
		return reason{"group_cannot_fit_together", nil}
	}
	return reason{"no_feasible_placement", nil}
}

func (r *unpackedReasons) provenByConstraint(item domain.ItemInstance, containers []*domain.Container) *reason {
	for _, rule := range r.customConstraints {
		proving, ok := rule.(constraint.ProvenRejection)
		if !ok {
			continue
		}
		if proof := proving.ProvesUnplaceable(item, containers); proof != nil {
			return &reason{proof.Code, []string{proof.Detail}}
		}
	}
	return nil
}

func fitsSomewhere(item domain.ItemInstance, containers []*domain.Container, rotations []domain.Rotation) bool {
	for _, container := range containers {
		for _, oriented := range item.Item().Dimensions().UniqueRotations(rotations) {
			if fitsAlone(container, oriented.Dimensions) {
				return true
			}
		}
	}
	return false
}

// supportIsTheBlocker reports whether the item would fit in an already-packed container's
// final layout once support alone is set aside, but not with it. An observed fact about this
// layout, not a proof about every layout, run on its own small fixed budget so it can never
// hang the solve.
func (r *unpackedReasons) supportIsTheBlocker(item domain.ItemInstance, packed []*domain.PackedContainer,
	cfg *config.PackingConfig) bool {
	constraints := defaultConstraints(cfg.MinimumSupportRatio(), r.customConstraints)
	canReject := false
	var withoutSupport []constraint.PlacementConstraint
	for _, rule := range constraints {
		if support, ok := rule.(*constraint.SupportConstraint); ok {
			canReject = canReject || support.CanReject(item)
			continue
		}
		withoutSupport = append(withoutSupport, rule)
	}
	if !canReject {
		return false
	}
	budget := DeadlineOfMilliseconds(2_000)
	for _, packedContainer := range packed {
		if budget.Expired() {
			return false
		}
		state := NewContainerState(packedContainer.Container(), packedContainer.Sequence())
		for _, placement := range packedContainer.Placements() {
			state.Add(placement)
		}
		if len(FindCandidates(state, item, cfg, withoutSupport, &SearchStats{}, budget, r.scorer, 1, nil)) == 0 {
			continue
		}
		if len(FindCandidates(state, item, cfg, constraints, &SearchStats{}, budget, r.scorer, 1, nil)) == 0 {
			return true
		}
	}
	return false
}
