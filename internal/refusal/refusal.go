// Package refusal holds the error a model raises when it refuses a value while being built on
// its own, before it is part of a request. It has no dependencies, so every layer that builds
// a model can return one; domain.ObjectRefusal is its public name.
package refusal

// Code is the code every object refusal carries, as an invalid request does.
const Code = "invalid_request"

// Error is a refused value with the request schema's reason for it ("below_minimum",
// "invalid_value", "not_allowed", "wrong_type", ...). It names no field: where the value sits
// in a request is not known to the model. Error() is the detail alone, the text the model
// has always returned, so a wrapper that quotes it reads what it read before.
type Error struct {
	Reason string
	Detail string
	cause  error
}

// New is a refusal with its reason and detail.
func New(reason, detail string) *Error { return &Error{Reason: reason, Detail: detail} }

// Wrap is a refusal that keeps the error it was read from as its cause.
func Wrap(reason, detail string, cause error) *Error {
	return &Error{Reason: reason, Detail: detail, cause: cause}
}

func (e *Error) Error() string { return e.Detail }

// Unwrap is the error the refusal was read from, if any.
func (e *Error) Unwrap() error { return e.cause }

// Ratio is the refusal of a ratio outside [0, 1]: below zero and above one are told apart,
// and anything else outside it (NaN) is still refused.
func Ratio(value float64, detail string) *Error {
	switch {
	case value < 0:
		return New("below_minimum", detail)
	case value > 1:
		return New("above_maximum", detail)
	}
	return New("invalid_value", detail)
}
