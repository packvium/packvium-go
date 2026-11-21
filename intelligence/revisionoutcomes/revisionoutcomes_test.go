package revisionoutcomes

import (
	"reflect"
	"testing"

	"github.com/packvium/packvium-go/intelligence/outcomes"
	"github.com/packvium/packvium-go/internal/jsonvalue"
	"github.com/packvium/packvium-go/revisions"
)

// After the reference's tests/test_revision_outcomes.py; the revision here is built by hand
// because a derived one needs a solve, which this package must not import.

func revision(t *testing.T) map[string]interface{} {
	t.Helper()
	value, err := jsonvalue.DecodeBytes([]byte(`{"format": "packvium-plan-revision/v1", "suite_version": "1.4.0",
	  "revision": 1, "parent": "sha256:00", "approved": null, "request": {},
	  "events": [
	    {"sequence": 1, "type": "placement_locked", "placement": {"item_type": "cube", "container_type": "box",
	      "orientation": "LWH"}},
	    {"sequence": 2, "type": "item_missing", "item_type": "cube", "quantity": 1},
	    {"sequence": 3, "type": "container_substituted", "container_type": "box", "replacement": {"id": "crate"}},
	    {"sequence": 4, "type": "placement_verified", "placement": {"item_type": "cube", "container_type": "crate",
	      "orientation": "LWH"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]interface{})
}

func TestEachExceptionBecomesOneLedgerEventNamedByItsRevision(t *testing.T) {
	document := revision(t)
	events, err := RevisionOutcomeEvents(document, "order-1", 500)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := revisions.DocumentDigest(document)
	var ids []string
	var types []outcomes.OutcomeEventType
	for _, event := range events {
		ids = append(ids, event.EventID())
		types = append(types, event.EventType())
	}
	if !reflect.DeepEqual(ids, []string{"order-1@" + digest + "#1", "order-1@" + digest + "#2",
		"order-1@" + digest + "#3", "order-1@" + digest + "#4"}) {
		t.Errorf("%v", ids)
	}
	if !reflect.DeepEqual(types, []outcomes.OutcomeEventType{outcomes.OperatorOverride, outcomes.ItemMissing,
		outcomes.ActualCarton, outcomes.PlacementVerified}) {
		t.Errorf("%v", types)
	}
	if payload := events[1].Payload(); !reflect.DeepEqual(payload, map[string]interface{}{"item_type": "cube",
		"quantity": int64(1)}) {
		t.Errorf("%v", payload)
	}
}

func TestTheRootRevisionRecordsNothing(t *testing.T) {
	document := revision(t)
	document["events"] = []interface{}{}
	if events, err := RevisionOutcomeEvents(document, "order-1", 500); err != nil || len(events) != 0 {
		t.Errorf("%v %v", events, err)
	}
}

func TestADocumentThatIsNotARevisionIsRefused(t *testing.T) {
	_, err := RevisionOutcomeEvents(map[string]interface{}{"events": []interface{}{}}, "order-1", 500)
	if refusal, ok := err.(*revisions.Error); !ok || refusal.Code != "invalid_revision" {
		t.Errorf("%#v", err)
	}
}

func TestRecordingARevisionTwiceChangesNothingAndLeavesItUntouched(t *testing.T) {
	document := revision(t)
	before := revision(t)
	ledger := outcomes.NewOutcomeLedger()
	first, err := RecordRevision(ledger, document, "order-1", 500)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RecordRevision(ledger, document, "order-1", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(again) || len(ledger.EventsForDecision("order-1")) != 4 {
		t.Errorf("%d then %d, ledger %d", len(first), len(again), len(ledger.EventsForDecision("order-1")))
	}
	for index := range first {
		if !first[index].Equal(again[index]) {
			t.Errorf("event %d changed on re-recording", index)
		}
	}
	if !reflect.DeepEqual(document, before) {
		t.Error("recording changed the revision")
	}
}
