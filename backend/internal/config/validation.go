package config

// ValidationError identifies a schema-owned field without echoing its value.
// Array indices are zero-based and separated by dots, like wifi.aps.1.psk.
type ValidationError struct {
	Path    string
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Path + ": " + e.Message }

func invalidField(path, message string) error {
	return &ValidationError{Path: path, Code: "invalid_field", Message: message}
}
