package domain

import "strings"

// The codes a request refusal carries.
const (
	InvalidRequestCode        = "invalid_request"
	InvalidFixedPlacementCode = "invalid_fixed_placement"
)

// RequestErrorReasons is the closed set of reasons every engine shares (docs/ERRORS.md). A
// fixed-placement refusal uses its own two, "malformed" and "cannot_hold".
var RequestErrorReasons = []string{"missing_field", "wrong_type", "below_minimum", "above_maximum",
	"negative_measure", "invalid_unit", "duplicate_id", "not_allowed", "invalid_value"}

// InvalidRequestError is a request no engine may answer; nothing was solved. Code is
// "invalid_request", or "invalid_fixed_placement" for a FixedPlacementError; Reason is one of
// a closed set; Field is the RFC 6901 pointer of the bad value, "" for the request as a whole;
// Detail is the fixed text for the reason, so the message is the same in every engine:
// "invalid_request: /items/0/quantity: must be at least 1".
type InvalidRequestError struct {
	Code   string
	Reason string
	Field  string
	Detail string
}

// NewInvalidRequestError is an "invalid_request" refusal.
func NewInvalidRequestError(reason, field, detail string) *InvalidRequestError {
	return &InvalidRequestError{InvalidRequestCode, reason, field, detail}
}

// NewFixedPlacementError is a refusal of the request's fixed placements: reason "malformed"
// with the bad value's pointer, or "cannot_hold" with "/fixed_placements". No search is
// attempted.
func NewFixedPlacementError(reason, field, detail string) *InvalidRequestError {
	return &InvalidRequestError{InvalidFixedPlacementCode, reason, field, detail}
}

// IsFixedPlacementError reports whether the refusal is about the fixed placements.
func (e *InvalidRequestError) IsFixedPlacementError() bool {
	return e.Code == InvalidFixedPlacementCode
}

func (e *InvalidRequestError) Error() string {
	if e.Code == InvalidFixedPlacementCode || e.Field == "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code + ": " + e.Field + ": " + e.Detail
}

var pointerEscapes = strings.NewReplacer("~", "~0", "/", "~1")

// Pointer is the RFC 6901 pointer to a value, escaping "~" and "/" in keys.
func Pointer(parts ...string) string {
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteByte('/')
		builder.WriteString(pointerEscapes.Replace(part))
	}
	return builder.String()
}
