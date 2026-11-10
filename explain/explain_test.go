package explain

import (
	"reflect"
	"testing"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/units"
)

func instance(t *testing.T, id string, length, width, height int64, sequence int) domain.ItemInstance {
	t.Helper()
	spec := domain.NewItemSpec(id, domain.MustDimensions(length*units.TicksPerMM, width*units.TicksPerMM, height*units.TicksPerMM))
	spec.Quantity = int64(sequence)
	item, err := domain.NewItem(spec)
	if err != nil {
		t.Fatal(err)
	}
	return item.Instances()[sequence-1]
}

// Expected sentences were produced by the PHP engine at tag v1.0.0.
func TestRendersTheEvidenceLevelAndDetails(t *testing.T) {
	a2, b1 := instance(t, "A", 50, 50, 50, 2), instance(t, "B", 50, 40, 30, 1)
	got, err := UnpackedItems([]domain.UnpackedItem{
		domain.NewUnpackedItem(a2, "time_limit", []string{"a", "b"}),
		domain.NewUnpackedItem(b1, "policy_rule", nil),
		domain.NewUnpackedItem(b1, "group_cannot_fit_together", nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"A#2: Unknown (limit reached): was not reached before the configured time limit expired (a; b)",
		"B#1: Proven: is forbidden from every offered container by a policy rule the request declared" +
			" -- the rule and version are in the details",
		"B#1: Inferred: belongs to a group that could not all be placed together",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestDescribesTheMessageForACallersCatalogue(t *testing.T) {
	descriptor, err := UnpackedItemDescriptor(domain.NewUnpackedItem(instance(t, "A", 50, 50, 50, 2), "time_limit", []string{"a", "b"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Descriptor{MessageKey: "packvium.unpacked.time_limit", ItemID: "A#2", EvidenceLevel: "unknown_due_to_limit",
		Details: "a; b", DefaultMessage: "was not reached before the configured time limit expired"}
	if descriptor != want {
		t.Fatalf("%+v", descriptor)
	}
}

func TestRefusesAnUnregisteredReason(t *testing.T) {
	_, err := Reason("nope")
	refusal, ok := err.(*UnknownReasonError)
	if !ok || refusal.Error() != "no explanation registered for reason code 'nope'" || refusal.Reason != "nope" {
		t.Fatalf("%v", err)
	}
}

func TestEveryReasonCodeHasAMessage(t *testing.T) {
	if len(ReasonCodes) != len(ReasonMessages) {
		t.Fatal("codes and messages disagree")
	}
	for _, code := range ReasonCodes {
		if _, err := Reason(code); err != nil {
			t.Fatal(err)
		}
	}
}
