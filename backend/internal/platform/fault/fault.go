// Package fault defines Alfred's errors: a small set of generic categories
// used throughout the app, so callers handle failures uniformly with
// errors.Is instead of depending on each package's or library's own error
// values. Front ends map the categories in one place (assistant.SpokenText
// for voice; an HTTPCode equivalent once there is an HTTP API).
//
// Raise a category with a message: errors.WithMessagef(fault.ErrNotFound,
// "intent %q", name). Classify an error from another package by prefixing
// it: errors.Prefix(err, fault.ErrDecoding). Define a package-specific error
// only when it is very specific, e.g. a type that carries data; make it
// unwrap to one of these categories.
package fault

import (
	"net/http"

	"github.com/peterbourgon/ff/v4"
	"gitlab.com/tozd/go/errors"
)

var (
	// ErrNotFound: the thing asked for does not exist (an intent, a
	// file, a matching phrase).
	ErrNotFound = errors.Base("not found")
	// ErrInvalidValue: a caller-supplied value is wrong (configuration,
	// a flag, an argument).
	ErrInvalidValue = errors.Base("invalid value")
	// ErrBadState: the operation is not allowed in the current state
	// (e.g. starting a service twice).
	ErrBadState = errors.Base("bad state")
	// ErrUnexpectedType: a value has a different type than required.
	ErrUnexpectedType = errors.Base("unexpected type")
	// ErrDecoding: data cannot be parsed (e.g. a malformed wav file).
	ErrDecoding = errors.Base("cannot decode value")
	// ErrTimeout: an operation took too long.
	ErrTimeout = errors.Base("timeout")
	// ErrTemporary: a transient failure; retrying later may succeed.
	ErrTemporary = errors.Base("temporary error")
	// ErrServiceUnavailable: an external service or tool cannot be
	// reached or is not installed.
	ErrServiceUnavailable = errors.Base("service unavailable")
	// ErrInternal: a failure inside Alfred that callers cannot act on.
	ErrInternal = errors.Base("internal")
	// ErrUnauthorized: credentials for an external service are missing,
	// expired or revoked (the user needs to log in again).
	ErrUnauthorized = errors.Base("unauthorized")
	// ErrPermission: the account may not do this (e.g. a feature that
	// needs a paid subscription).
	ErrPermission = errors.Base("forbidden")
	// ErrRateLimited: an external service asks us to slow down.
	ErrRateLimited = errors.Base("rate limited")

	// ErrRequiredFlags: required command line flags are missing.
	ErrRequiredFlags = errors.BaseWrap(ff.ErrHelp, "missing required flags")
)

// ErrorFromHTTPCode returns the category for an external service's HTTP
// status code, nil for 2xx (as in the greenfield backend). Wrap the result
// with the context: errors.WithMessagef(fault.ErrorFromHTTPCode(code), ...).
func ErrorFromHTTPCode(code int) error {
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized:
		return ErrUnauthorized
	case code == http.StatusForbidden:
		return ErrPermission
	case code == http.StatusNotFound:
		return ErrNotFound
	case code == http.StatusRequestTimeout, code == http.StatusGatewayTimeout:
		return ErrTimeout
	case code == http.StatusTooManyRequests:
		return ErrRateLimited
	case code >= 500:
		// An upstream server error is transient from our side.
		return ErrTemporary
	default:
		return ErrInvalidValue
	}
}
