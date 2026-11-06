package model

import "github.com/wisnuvb/ronda/i18n"

// ErrNotFound is returned when no site has the id. It is a comparable value, so errors.Is works, and it
// translates itself when shown.
var ErrNotFound error = notFoundError{}

type notFoundError struct{}

func (notFoundError) Error() string { return i18n.T("site not found") }

// InvalidError means the caller sent something unacceptable (maps to HTTP 400).
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Invalid wraps an already translated message in an InvalidError.
func Invalid(msg string) error { return &InvalidError{Msg: msg} }
