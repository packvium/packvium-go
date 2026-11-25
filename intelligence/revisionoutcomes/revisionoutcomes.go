// Package revisionoutcomes turns approved plan revisions into labelled evidence for historical
// evaluation.
//
// A revision records what differed from an approved plan. This package appends those records
// to an outcomes.OutcomeLedger, where package holdout already evaluates recommendations on
// untouched history; it adds no second evaluator and no second store.
//
//   - Traceable: every ledger event's id is the decision, the SHA-256 of the revision that
//     recorded it and the event's sequence, so the evidence behind a recommendation names one
//     hashed, versioned document. The decision is part of the id because ids are global.
//   - Append-only: recording only calls OutcomeLedger.Record, which never replaces an event,
//     and neither the plan nor the revision is read back or changed. Re-recording a revision
//     is idempotent.
//   - Learning cannot reach a result: no solver, validator or request reads the ledger.
//
// A revision takes no clock, so the integrator passes recordedAt from its own system.
package revisionoutcomes

import (
	"math/big"
	"strconv"

	"github.com/packvium/packvium-go/intelligence/outcomes"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/revisions"
)

var eventTypes = map[string]outcomes.OutcomeEventType{
	"item_missing":          outcomes.ItemMissing,
	"container_substituted": outcomes.ActualCarton,
	// A lock is an operator deciding where an item goes instead of the plan, which is what an
	// override is, and why holdout reports it against the baseline, never the treatment.
	"placement_locked":   outcomes.OperatorOverride,
	"placement_verified": outcomes.PlacementVerified,
}

var payloads = map[string]func(map[string]interface{}) map[string]interface{}{
	"item_missing": func(event map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"item_type": event["item_type"], "quantity": event["quantity"]}
	},
	"container_substituted": func(event map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"carton_id": event["replacement"].(map[string]interface{})["id"]}
	},
	"placement_locked": func(map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"reason_code": "placement_locked"}
	},
	"placement_verified": func(event map[string]interface{}) map[string]interface{} {
		placement := event["placement"].(map[string]interface{})
		return map[string]interface{}{"item_type": placement["item_type"], "container_type": placement["container_type"]}
	},
}

// RevisionOutcomeEvents are the ledger events one revision's exceptions become; the root
// records none. A document that is not a revision is refused with *revisions.Error.
func RevisionOutcomeEvents(revision interface{}, decisionID string, recordedAt int64) ([]outcomes.OutcomeEvent, error) {
	document, ok := revision.(map[string]interface{})
	if !ok || document["format"] != revisions.Format {
		return nil, &revisions.Error{Code: "invalid_revision", Message: "not a " + revisions.Format + " document"}
	}
	digest, err := revisions.DocumentDigest(document)
	if err != nil {
		return nil, err
	}
	recorded, _ := document["events"].([]interface{})
	events := make([]outcomes.OutcomeEvent, 0, len(recorded))
	for _, raw := range recorded {
		event, _ := raw.(map[string]interface{})
		kind, _ := event["type"].(string)
		payload, known := payloads[kind]
		if !known {
			return nil, &revisions.Error{Code: "invalid_event", Message: "a revision event has no outcome type"}
		}
		outcome, err := outcomes.NewOutcomeEvent(outcomes.OutcomeEventSpec{
			EventID: decisionID + "@" + digest + "#" + sequenceText(event["sequence"]), DecisionID: decisionID,
			EventType: eventTypes[kind], Payload: payload(event), RecordedAt: recordedAt,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, outcome)
	}
	return events, nil
}

// RecordRevision appends one revision's exceptions to ledger and returns what the ledger
// holds for them.
func RecordRevision(ledger *outcomes.OutcomeLedger, revision interface{}, decisionID string,
	recordedAt int64) ([]outcomes.OutcomeEvent, error) {
	events, err := RevisionOutcomeEvents(revision, decisionID, recordedAt)
	if err != nil {
		return nil, err
	}
	held := make([]outcomes.OutcomeEvent, len(events))
	for index, event := range events {
		if held[index], err = ledger.Record(event); err != nil {
			return nil, err
		}
	}
	return held, nil
}

// sequenceText is the sequence as the reference formats it into an id.
func sequenceText(value interface{}) string {
	switch typed := value.(type) {
	case int64:
		return strconv.FormatInt(typed, 10)
	case *big.Int:
		return typed.String()
	case float64:
		return jsonvalue.FloatRepr(typed)
	}
	return ""
}
