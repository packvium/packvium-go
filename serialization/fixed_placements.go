package serialization

import (
	"strconv"

	"github.com/packvium/packvium-go/domain"
	"github.com/packvium/packvium-go/internal/canonicaljson"
	"github.com/packvium/packvium-go/internal/pointshape"
)

var (
	fixedRequired = []string{"item_type", "container_type", "orientation"}
	fixedFields   = []string{"item_type", "container_type", "orientation", "container_instance", "position"}
)

func malformed(detail, field string) error {
	return domain.NewFixedPlacementError("malformed", field, detail)
}

// decodeFixedPlacements reads the request's fixed placements, or refuses the first entry that
// is not the schema's shape. Nothing is coerced: "1" and true are not instances, and a list is
// not a position. An absent or null field is no fixed placements. O(f).
func decodeFixedPlacements(raw interface{}, unit string) ([]domain.FixedPlacement, error) {
	if raw == nil {
		return nil, nil
	}
	entries, ok := raw.([]interface{})
	if !ok {
		return nil, malformed("fixed_placements is a list", "/fixed_placements")
	}
	for index, entry := range entries {
		where := "fixed_placements[" + strconv.Itoa(index) + "]"
		if err := requireFixedShape(entry, where, domain.Pointer("fixed_placements", strconv.Itoa(index)), unit); err != nil {
			return nil, err
		}
	}
	fixed := make([]domain.FixedPlacement, len(entries))
	for index, entry := range entries {
		placement, err := buildFixedPlacement(entry.(map[string]interface{}), unit)
		if err != nil {
			return nil, err
		}
		fixed[index] = placement
	}
	return fixed, nil
}

func requireFixedShape(raw interface{}, where, field, unit string) error {
	entry, ok := raw.(map[string]interface{})
	if !ok {
		return malformed(where+" is an object", field)
	}
	if unknown := pointshape.UnknownKeys(entry, fixedFields); len(unknown) > 0 {
		return malformed(where+" cannot carry "+canonicaljson.Spelling(unknown), field)
	}
	var missing []interface{}
	for _, name := range fixedRequired {
		if _, present := entry[name]; !present {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return malformed(where+" needs "+canonicaljson.Spelling(missing), field)
	}
	for _, name := range []string{"item_type", "container_type"} {
		if text, ok := entry[name].(string); !ok || text == "" {
			return malformed(where+"."+name+" is a non-empty string", field+"/"+name)
		}
	}
	if !pointshape.IsOrientation(entry["orientation"]) {
		return malformed(where+".orientation "+pointshape.OrientationRule(), field+"/orientation")
	}
	if instance, ok := containerInstance(entry); !ok || instance < 1 {
		return malformed(where+".container_instance counts from 1", field+"/container_instance")
	}
	position := positionOf(entry)
	if err := pointshape.RequirePoint(position, where+".position", field+"/position", malformed); err != nil {
		return err
	}
	point := position.(map[string]interface{})
	for _, axis := range pointshape.Axes {
		value, present := point[axis]
		if !present {
			continue
		}
		if _, err := parseLength(value, unit); err != nil {
			if isNegative(err) {
				return malformed(where+".position."+axis+" cannot be negative", field+"/position/"+axis)
			}
			return malformed(where+".position."+axis+" is a measure", field+"/position/"+axis)
		}
	}
	return nil
}

func containerInstance(entry map[string]interface{}) (int64, bool) {
	value, present := entry["container_instance"]
	if !present {
		return 1, true
	}
	return canonicaljson.Integer(value)
}

func positionOf(entry map[string]interface{}) interface{} {
	position, present := entry["position"]
	if !present {
		return map[string]interface{}{}
	}
	return position
}

func buildFixedPlacement(entry map[string]interface{}, unit string) (domain.FixedPlacement, error) {
	point := positionOf(entry).(map[string]interface{})
	var ticks [3]int64
	for index, axis := range pointshape.Axes {
		value, present := point[axis]
		if !present {
			continue
		}
		length, err := parseLength(value, unit)
		if err != nil {
			return domain.FixedPlacement{}, err
		}
		ticks[index] = length.Ticks()
	}
	position, err := domain.NewPoint(ticks[0], ticks[1], ticks[2])
	if err != nil {
		return domain.FixedPlacement{}, err
	}
	rotation, err := domain.ParseRotation(entry["orientation"].(string))
	if err != nil {
		return domain.FixedPlacement{}, err
	}
	instance, _ := containerInstance(entry)
	return domain.NewFixedPlacement(entry["item_type"].(string), entry["container_type"].(string), position,
		rotation, instance)
}
