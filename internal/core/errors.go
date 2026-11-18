package core

import (
	"encoding/json"
	"errors"
)

// Error codes are stable strings so that the client can translate them and
// the HTTP layer can carry them between processes.
const (
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeInvalid      = "invalid"
	CodeAuth         = "auth"
	CodeConflict     = "conflict"
	CodeTooLarge     = "too_large"
	CodeEditExpired  = "edit_expired"
	CodeColumnNotEmp = "column_not_empty"
	CodeOffline      = "offline"
	CodeInternal     = "internal"
)

type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return e.Code
}

func errf(code, msg string) error { return &Error{Code: code, Msg: msg} }

var (
	ErrForbidden = &Error{Code: CodeForbidden}
	ErrNotFound  = &Error{Code: CodeNotFound}
	ErrAuth      = &Error{Code: CodeAuth}
)

// CodeOf returns the code of a core error, or CodeInternal.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
