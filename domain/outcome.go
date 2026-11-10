package domain

import (
	"errors"
	"fmt"
)

// RejectionObservation is one reason code observed while trying to place an item.
type RejectionObservation struct {
	Code    string
	Count   int64
	Details []string
}

// ToMap is the wire form.
func (o RejectionObservation) ToMap() map[string]interface{} {
	return map[string]interface{}{"code": o.Code, "count": o.Count, "details": stringList(o.Details)}
}

// ReasonProof is how strongly an unpacked reason is established.
type ReasonProof struct {
	Level        string
	Observations []RejectionObservation
}

// ProofForReason grades a reason: `policy_rule` is proven, because it is only reported when
// a rule rules the item out of every offered container.
func ProofForReason(reason string, details []string) ReasonProof {
	level := "inferred"
	switch reason {
	case "no_compatible_container_dimensions", "payload_exceeded", "no_eligible_container", "rotation_restricted", "policy_rule":
		level = "proven"
	case "time_limit", "effort_limit":
		level = "unknown_due_to_limit"
	case "no_feasible_placement", "search_exhausted", "exact_search_incomplete", "insufficient_support":
		level = "observed"
	}
	return ReasonProof{level, []RejectionObservation{{reason, 1, append([]string(nil), details...)}}}
}

// ToMap is the wire form.
func (p ReasonProof) ToMap() map[string]interface{} {
	observations := make([]interface{}, len(p.Observations))
	for index, observation := range p.Observations {
		observations[index] = observation.ToMap()
	}
	return map[string]interface{}{"level": p.Level, "observations": observations}
}

// UnpackedItem is an instance left out of every container, with the reason and its proof.
type UnpackedItem struct {
	Instance ItemInstance
	Reason   string
	Details  []string
	Proof    ReasonProof
}

// NewUnpackedItem derives the proof from the reason.
func NewUnpackedItem(instance ItemInstance, reason string, details []string) UnpackedItem {
	return UnpackedItem{instance, reason, append([]string(nil), details...), ProofForReason(reason, details)}
}

func stringList(values []string) []interface{} {
	list := make([]interface{}, len(values))
	for index, value := range values {
		list[index] = value
	}
	return list
}

// PackingRequest is the items and containers of one request.
type PackingRequest struct {
	items      []*Item
	containers []*Container
}

// NewPackingRequest refuses an empty side or duplicate ids.
func NewPackingRequest(items []*Item, containers []*Container) (*PackingRequest, error) {
	if len(items) == 0 {
		return nil, errors.New("at least one item is required")
	}
	if len(containers) == 0 {
		return nil, errors.New("at least one container is required")
	}
	itemIDs := map[string]bool{}
	for _, item := range items {
		if itemIDs[item.ID()] {
			return nil, errors.New("item ids must be unique")
		}
		itemIDs[item.ID()] = true
	}
	containerIDs := map[string]bool{}
	for _, container := range containers {
		if containerIDs[container.ID()] {
			return nil, errors.New("container ids must be unique")
		}
		containerIDs[container.ID()] = true
	}
	return &PackingRequest{append([]*Item(nil), items...), append([]*Container(nil), containers...)}, nil
}

func (r *PackingRequest) Items() []*Item           { return append([]*Item(nil), r.items...) }
func (r *PackingRequest) Containers() []*Container { return append([]*Container(nil), r.containers...) }

// Instances are every physical copy, in item order then sequence order. O(total quantity).
func (r *PackingRequest) Instances() []ItemInstance {
	var all []ItemInstance
	for _, item := range r.items {
		all = append(all, item.Instances()...)
	}
	return all
}

// AllDirections are the six faces a box can leave a container through, in a fixed order
// callers rely on when they pick the first clear one.
var AllDirections = []string{"+x", "-x", "+y", "-y", "+z", "-z"}

// Sweep is a swept region [x1, y1, z1, x2, y2, z2] in ticks.
type Sweep [6]int64

// SweptVolume is the region between a box's face and the container wall along direction.
// The same region serves loading and unloading.
func SweptVolume(box Box, container Dimensions, direction string) (Sweep, error) {
	x1, y1, z1, x2, y2, z2 := box.origin.x, box.origin.y, box.origin.z, box.X2(), box.Y2(), box.Z2()
	switch direction {
	case "+x":
		x1, x2 = x2, container.length.Ticks()
	case "-x":
		x1, x2 = 0, x1
	case "+y":
		y1, y2 = y2, container.width.Ticks()
	case "-y":
		y1, y2 = 0, y1
	case "+z":
		z1, z2 = z2, container.height.Ticks()
	case "-z":
		z1, z2 = 0, z1
	default:
		return Sweep{}, fmt.Errorf("unknown movement direction '%s'; expected one of ('+x', '-x', '+y', '-y', '+z', '-z')", direction)
	}
	return Sweep{x1, y1, z1, x2, y2, z2}, nil
}

// Intersects is half-open on every axis, so a box flush against a corridor's face does not
// block it.
func (s Sweep) Intersects(box Box) bool {
	return s[0] < box.X2() && box.origin.x < s[3] && s[1] < box.Y2() && box.origin.y < s[4] &&
		s[2] < box.Z2() && box.origin.z < s[5]
}
