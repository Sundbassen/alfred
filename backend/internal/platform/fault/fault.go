// Package fault maps errors from external libraries to Alfred's own typed
// errors, so callers handle them uniformly with errors.Is instead of
// depending on each library's error values. Each error wraps the external
// error it stands for (errors.BaseWrap keeps it matchable).
package fault

import (
	"github.com/peterbourgon/ff/v4"
	"gitlab.com/tozd/go/errors"
)

var ErrRequiredFlags = errors.BaseWrap(ff.ErrHelp, "missing required flags")
