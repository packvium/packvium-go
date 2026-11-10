package domain

import "github.com/packvium/packvium-go/internal/pyrepr"

// InvalidDirectionCode is the stable discriminator an invalid direction carries on the wire.
const InvalidDirectionCode = "invalid_direction"

// InvalidDirectionError is a movement direction outside the six-value vocabulary. It is
// refused rather than read as one of the six. It lives beside SweptVolume because both the
// constraint layer and the sequence layer refuse it, and the sequence layer sits above the
// constraint layer.
type InvalidDirectionError struct{ direction string }

// NewInvalidDirectionError names the refused direction.
func NewInvalidDirectionError(direction string) *InvalidDirectionError {
	return &InvalidDirectionError{direction}
}

// Direction is the refused value.
func (e *InvalidDirectionError) Direction() string { return e.direction }

func (e *InvalidDirectionError) Error() string {
	return "unknown movement direction " + pyrepr.String(e.direction) +
		"; expected one of ('+x', '-x', '+y', '-y', '+z', '-z')"
}

// ToMap is the wire form.
func (e *InvalidDirectionError) ToMap() map[string]interface{} {
	return map[string]interface{}{"code": InvalidDirectionCode, "direction": e.direction}
}

// IsDirection reports whether direction is one of AllDirections.
func IsDirection(direction string) bool {
	for _, known := range AllDirections {
		if known == direction {
			return true
		}
	}
	return false
}

// ValidateDirections refuses the first direction outside the vocabulary. O(d).
func ValidateDirections(directions []string) error {
	for _, direction := range directions {
		if !IsDirection(direction) {
			return NewInvalidDirectionError(direction)
		}
	}
	return nil
}
