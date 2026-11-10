// Package explain renders deterministic, localization-ready sentences for why an item was not
// packed. Rendering is driven entirely by an unpacked item's reason and proof, the structured
// evidence already attached to it, never by parsing free text. ReasonMessages is the one place
// English prose lives; a caller wanting another locale swaps that table, not the code that
// walks the evidence.
package explain

import (
	"strings"

	"github.com/toxakara/packvium-go/domain"
)

// ReasonCodes are the reason codes with a registered message, in registration order.
var ReasonCodes = []string{
	"no_compatible_container_dimensions", "rotation_restricted", "payload_exceeded", "policy_rule",
	"no_eligible_container", "time_limit", "effort_limit", "group_cannot_fit_together", "insufficient_support",
	"no_feasible_placement", "search_exhausted", "exact_search_incomplete", "container_inventory_exhausted",
}

// ReasonMessages is one deterministic English sentence per reason code. Every reason the
// library can produce has an entry: Reason refuses rather than falling back to the bare code,
// so a new code without a message is caught immediately, not shipped mute.
var ReasonMessages = map[string]string{
	"no_compatible_container_dimensions": "does not fit inside any offered container in any rotation",
	"rotation_restricted": "would fit in some container with more rotations allowed, but not with the " +
		"rotations this item permits",
	"payload_exceeded": "exceeds the maximum payload of every offered container",
	"policy_rule": "is forbidden from every offered container by a policy rule the request " +
		"declared -- the rule and version are in the details",
	"no_eligible_container":     "shares no eligible container tag with any offered container",
	"time_limit":                "was not reached before the configured time limit expired",
	"effort_limit":              "was not reached before the configured effort budget was exhausted",
	"group_cannot_fit_together": "belongs to a group that could not all be placed together",
	"insufficient_support": "would fit geometrically, but only by resting on support the minimum " +
		"support ratio forbids",
	"no_feasible_placement": "found no feasible placement in the containers offered, for a reason the " +
		"search could not further isolate",
	"search_exhausted":              "was not placed before the configured search strategies were exhausted",
	"exact_search_incomplete":       "was not placed because the exact search ended before proving a final answer",
	"container_inventory_exhausted": "requires another compatible container, but the declared inventory is exhausted",
}

// LevelPrefixes prefix an explanation with the kind of evidence behind it: a caller deciding
// whether to trust "this will never fit" (proven) or "try again with more time"
// (unknown_due_to_limit) needs the distinction as much as the sentence.
var LevelPrefixes = map[string]string{
	"proven":               "Proven",
	"unknown_due_to_limit": "Unknown (limit reached)",
	"observed":             "Observed",
	"inferred":             "Inferred",
}

// UnknownReasonError is a reason code with no registered message.
type UnknownReasonError struct{ Reason string }

func (e *UnknownReasonError) Error() string {
	return "no explanation registered for reason code '" + e.Reason + "'"
}

// Reason is the bare sentence for one reason code, with no item identity or evidence prefix.
func Reason(reason string) (string, error) {
	message, ok := ReasonMessages[reason]
	if !ok {
		return "", &UnknownReasonError{reason}
	}
	return message, nil
}

// UnpackedItem is one sentence naming the item, its evidence level and why it was not packed,
// e.g. "ladder#1: Proven: does not fit inside any offered container in any rotation". Details
// already attached are appended verbatim, in order.
func UnpackedItem(item domain.UnpackedItem) (string, error) {
	sentence, err := Reason(item.Reason)
	if err != nil {
		return "", err
	}
	lead := ""
	if prefix := LevelPrefixes[item.Proof.Level]; prefix != "" {
		lead = prefix + ": "
	}
	detail := ""
	if len(item.Details) > 0 {
		detail = " (" + strings.Join(item.Details, "; ") + ")"
	}
	return item.Instance.ID() + ": " + lead + sentence + detail, nil
}

// UnpackedItems is UnpackedItem over every item, in the order given.
func UnpackedItems(items []domain.UnpackedItem) ([]string, error) {
	out := make([]string, len(items))
	for index, item := range items {
		sentence, err := UnpackedItem(item)
		if err != nil {
			return nil, err
		}
		out[index] = sentence
	}
	return out, nil
}

// Descriptor is the message key, its arguments and the English default, for a caller's own
// catalogue.
type Descriptor struct {
	MessageKey     string
	ItemID         string
	EvidenceLevel  string
	Details        string
	DefaultMessage string
}

// UnpackedItemDescriptor describes the message for a caller's own catalogue.
func UnpackedItemDescriptor(item domain.UnpackedItem) (Descriptor, error) {
	message, err := Reason(item.Reason)
	if err != nil {
		return Descriptor{}, err
	}
	return Descriptor{
		MessageKey: "packvium.unpacked." + item.Reason, ItemID: item.Instance.ID(), EvidenceLevel: item.Proof.Level,
		Details: strings.Join(item.Details, "; "), DefaultMessage: message,
	}, nil
}
