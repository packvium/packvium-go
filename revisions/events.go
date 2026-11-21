package revisions

import (
	"github.com/packvium/packvium-go/internal/canonicaljson"
	"github.com/packvium/packvium-go/internal/pointshape"
)

var eventFields = map[string][]string{
	"item_missing":          {"item_type", "quantity"},
	"container_substituted": {"container_type", "replacement"},
	"placement_locked":      {"placement"},
	"placement_verified":    {"placement"},
}

var placementFields = []string{"item_type", "container_type", "container_instance", "position", "orientation"}

func requireShape(raw interface{}) (map[string]interface{}, error) {
	event, ok := raw.(map[string]interface{})
	if !ok {
		return nil, refuse("invalid_event", "an event is a JSON object")
	}
	kind, _ := event["type"].(string)
	fields, known := eventFields[kind]
	if !known {
		return nil, refuse("invalid_event", "unknown event type %s", canonicaljson.Spelling(event["type"]))
	}
	allowed := append([]string{"sequence", "type"}, fields...)
	if unknown := pointshape.UnknownKeys(event, allowed); len(unknown) > 0 {
		return nil, refuse("invalid_event", "%s does not carry %s", kind, canonicaljson.Spelling(unknown))
	}
	var missing []interface{}
	for _, field := range fields {
		if _, present := event[field]; !present {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return nil, refuse("invalid_event", "%s needs %s", kind, canonicaljson.Spelling(missing))
	}
	return event, validators[kind](event)
}

var validators = map[string]func(map[string]interface{}) error{
	"item_missing":          validateItemMissing,
	"container_substituted": validateContainerSubstituted,
	"placement_locked":      validatePlacement,
	"placement_verified":    validatePlacement,
}

func validateItemMissing(event map[string]interface{}) error {
	if err := requireName(event["item_type"], "item_type"); err != nil {
		return err
	}
	if quantity, ok := canonicaljson.Integer(event["quantity"]); !ok || quantity < 1 {
		return refuse("invalid_event", "item_missing.quantity is a positive integer")
	}
	return nil
}

func validateContainerSubstituted(event map[string]interface{}) error {
	if err := requireName(event["container_type"], "container_type"); err != nil {
		return err
	}
	replacement, ok := event["replacement"].(map[string]interface{})
	if !ok {
		return refuse("invalid_event", "a replacement is a container object")
	}
	return requireName(replacement["id"], "replacement.id")
}

func validatePlacement(event map[string]interface{}) error {
	placement, ok := event["placement"].(map[string]interface{})
	if !ok {
		return refuse("invalid_event", "a placement is a fixed-placement object")
	}
	if unknown := pointshape.UnknownKeys(placement, placementFields); len(unknown) > 0 {
		return refuse("invalid_event", "a placement does not carry %s", canonicaljson.Spelling(unknown))
	}
	if err := requireName(placement["item_type"], "placement.item_type"); err != nil {
		return err
	}
	if err := requireName(placement["container_type"], "placement.container_type"); err != nil {
		return err
	}
	if !pointshape.IsOrientation(placement["orientation"]) {
		return refuse("invalid_event", "placement.orientation is one of the six codes")
	}
	instance := interface{}(int64(1))
	if given, present := placement["container_instance"]; present {
		instance = given
	}
	if number, ok := canonicaljson.Integer(instance); !ok || number < 1 {
		return refuse("invalid_event", "placement.container_instance counts from 1")
	}
	position := interface{}(map[string]interface{}{})
	if given, present := placement["position"]; present {
		position = given
	}
	return pointshape.RequirePoint(position, "placement.position", "", invalidEvent)
}

func invalidEvent(message, _ string) error { return refuse("invalid_event", "%s", message) }

func requireName(value interface{}, field string) error {
	if text, ok := value.(string); !ok || text == "" {
		return refuse("invalid_event", "%s is a non-empty string", field)
	}
	return nil
}

// ApplyEvents is the request these events derive from request. It refuses a contradiction,
// never physics: whether the new fixed set can hold is the engine's admission check when the
// request is solved.
func ApplyEvents(request, events interface{}) (map[string]interface{}, error) {
	fields, ok := request.(map[string]interface{})
	if !ok {
		return nil, refuse("invalid_revision", "a request is a JSON object")
	}
	list, ok := events.([]interface{})
	if !ok {
		return nil, refuse("invalid_event", "events are a JSON array")
	}
	derived := copyObject(fields)
	index := &placementIndex{}
	for _, raw := range list {
		event, err := requireShape(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := canonicaljson.Integer(event["sequence"]); !ok {
			return nil, refuse("invalid_event", "event sequence %s is not an integer",
				canonicaljson.Spelling(event["sequence"]))
		}
		switch event["type"] {
		case "item_missing":
			err = applyItemMissing(derived, event)
		case "container_substituted":
			err = applyContainerSubstituted(derived, event)
		default:
			err = index.apply(derived, event)
		}
		if err != nil {
			return nil, err
		}
	}
	return derived, nil
}

func eventSequence(event map[string]interface{}) int64 {
	sequence, _ := canonicaljson.Integer(event["sequence"])
	return sequence
}

func applyItemMissing(request, event map[string]interface{}) error {
	items, err := entries(request, "items")
	if err != nil {
		return err
	}
	itemType := event["item_type"].(string)
	index, err := indexOf(items, itemType, "item")
	if err != nil {
		return err
	}
	stated := interface{}(int64(1))
	if given, present := items[index].(map[string]interface{})["quantity"]; present {
		stated = given
	}
	quantity, ok := canonicaljson.Integer(stated)
	if !ok {
		return refuse("invalid_revision", "request.items[%d].quantity is an integer", index)
	}
	missing, _ := canonicaljson.Integer(event["quantity"])
	remaining := quantity - missing
	fixed, err := fixedEntries(request)
	if err != nil {
		return err
	}
	count := int64(0)
	for _, entry := range fixed {
		if entry.(map[string]interface{})["item_type"] == itemType {
			count++
		}
	}
	if remaining < count {
		left := remaining
		if left < 0 {
			left = 0
		}
		return refuse("event_conflict", "event %d: %d %s are fixed, %d would remain", eventSequence(event), count,
			itemType, left)
	}
	if remaining > 0 {
		items[index].(map[string]interface{})["quantity"] = remaining
		return nil
	}
	if len(items) == 1 {
		return refuse("event_conflict", "event %d: no item would remain to pack", eventSequence(event))
	}
	request["items"] = append(append([]interface{}(nil), items[:index]...), items[index+1:]...)
	return nil
}

func applyContainerSubstituted(request, event map[string]interface{}) error {
	containers, err := entries(request, "containers")
	if err != nil {
		return err
	}
	containerType := event["container_type"].(string)
	index, err := indexOf(containers, containerType, "container")
	if err != nil {
		return err
	}
	fixed, err := fixedEntries(request)
	if err != nil {
		return err
	}
	for _, entry := range fixed {
		if entry.(map[string]interface{})["container_type"] == containerType {
			return refuse("event_conflict", "event %d: fixed placements are in %s", eventSequence(event), containerType)
		}
	}
	replacement := event["replacement"].(map[string]interface{})
	replacementID := replacement["id"].(string)
	for position, container := range containers {
		if position != index && container.(map[string]interface{})["id"] == replacementID {
			return refuse("event_conflict", "event %d: another container is already %s", eventSequence(event),
				replacementID)
		}
	}
	containers[index] = copyObject(replacement)
	return nil
}

// entries is the request's list under key, refused unless every entry is an object. The list
// is the derived request's own copy, so editing it in place edits only the derivation.
func entries(request map[string]interface{}, key string) ([]interface{}, error) {
	list, ok := request[key].([]interface{})
	if !ok {
		return nil, refuse("invalid_revision", "request.%s is a list of objects", key)
	}
	for _, entry := range list {
		if _, ok := entry.(map[string]interface{}); !ok {
			return nil, refuse("invalid_revision", "request.%s is a list of objects", key)
		}
	}
	return list, nil
}

// fixedEntries is the request's fixed placements; absent or null is none, and anything else
// not a list of objects is a request no event can safely edit.
func fixedEntries(request map[string]interface{}) ([]interface{}, error) {
	if request["fixed_placements"] == nil {
		return nil, nil
	}
	return entries(request, "fixed_placements")
}

func indexOf(list []interface{}, identifier, kind string) (int, error) {
	for index, entry := range list {
		if entry.(map[string]interface{})["id"] == identifier {
			return index, nil
		}
	}
	return 0, refuse("event_conflict", "the request has no %s %s", kind, canonicaljson.Spelling(identifier))
}

// placementIndex is one replay's set of fixed placements by canonical key, so a repeated lock
// is found in O(1) rather than by a scan; the request's first-seen order stays untouched.
type placementIndex struct {
	fixed     []interface{}
	keys      map[string]bool
	started   bool
	indexable bool
}

func withDefaultInstance(placement map[string]interface{}) map[string]interface{} {
	keyed := copyObject(placement)
	if _, present := keyed["container_instance"]; !present {
		keyed["container_instance"] = int64(1)
	}
	return keyed
}

func (p *placementIndex) start(request map[string]interface{}) error {
	fixed, err := fixedEntries(request)
	if err != nil {
		return err
	}
	p.fixed, p.keys, p.started, p.indexable = append([]interface{}{}, fixed...), map[string]bool{}, true, true
	for _, entry := range p.fixed {
		key, err := CanonicalJSON(withDefaultInstance(entry.(map[string]interface{})))
		if err != nil {
			p.indexable = false
			break
		}
		p.keys[key] = true
	}
	return nil
}

func (p *placementIndex) apply(request, event map[string]interface{}) error {
	if !p.started {
		if err := p.start(request); err != nil {
			return err
		}
	}
	given := event["placement"].(map[string]interface{})
	placement := withDefaultInstance(given)
	if p.indexable {
		key, err := CanonicalJSON(placement)
		if err == nil {
			if !p.keys[key] {
				p.keys[key] = true
				p.add(request, given)
			}
			return nil
		}
		p.indexable = false
	}
	// An uncanonicalizable value can reach a direct ApplyEvents call: keep the first-match
	// order of refusals rather than making the index an earlier one.
	for _, entry := range p.fixed {
		same, err := sameValue(withDefaultInstance(entry.(map[string]interface{})), placement)
		if err != nil {
			return err
		}
		if same {
			return nil
		}
	}
	p.add(request, given)
	return nil
}

func (p *placementIndex) add(request map[string]interface{}, placement map[string]interface{}) {
	p.fixed = append(p.fixed, copyObject(placement))
	request["fixed_placements"] = p.fixed
}
