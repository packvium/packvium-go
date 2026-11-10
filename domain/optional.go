package domain

import "github.com/packvium/packvium-go/units"

// The copy helpers below give every optional field its own storage, so neither the caller's
// spec nor a value handed out by an accessor can change an item or container afterwards.
// They are typed one by one because the module supports Go versions without generics.

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	duplicate := *value
	return &duplicate
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	duplicate := *value
	return &duplicate
}

func copyWeight(value *units.Weight) *units.Weight {
	if value == nil {
		return nil
	}
	duplicate := *value
	return &duplicate
}

func copyLength(value *units.Length) *units.Length {
	if value == nil {
		return nil
	}
	duplicate := *value
	return &duplicate
}

func copyDimensions(value *Dimensions) *Dimensions {
	if value == nil {
		return nil
	}
	duplicate := *value
	return &duplicate
}

func copyAxles(value *[2]Axle) *[2]Axle {
	if value == nil {
		return nil
	}
	duplicate := *value
	for index := range duplicate {
		duplicate[index].maxLoad = copyWeight(duplicate[index].maxLoad)
	}
	return &duplicate
}

func anyAtMost(values []int64, limit int64) bool {
	for _, value := range values {
		if value <= limit {
			return true
		}
	}
	return false
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func rotationIndex(rotation Rotation) int {
	for index, candidate := range AllRotations() {
		if candidate == rotation {
			return index
		}
	}
	return -1
}
