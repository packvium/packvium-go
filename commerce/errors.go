package commerce

// InputError reports a malformed document or request: a caller bug such as a missing
// key, a negative weight or an unknown policy operator.
//
// A rejection the commercial model is entitled to make -- no tariff effective at that
// instant, no rate for that zone -- is not an error at all. It is a successful call whose
// result document has status "rejected". Any other error a function returns is a
// failure the reference raises outside its input-error type (a Python TypeError when a
// predicate compares operands Python refuses to compare, for instance).
type InputError struct {
	Message string
}

func (err *InputError) Error() string {
	return err.Message
}

func inputError(path string, message string) error {
	return &InputError{Message: path + ": " + message}
}

// rejection carries a structured rejection from the point of detection to the API
// boundary, where it becomes the {"status": "rejected"} document. It never escapes.
type rejection struct {
	code   string
	fields *Object
}

func (err *rejection) Error() string {
	return err.code
}
