package fault

import (
	"github.com/peterbourgon/ff/v4"
	"gitlab.com/tozd/go/errors"
)

var ErrRequiredFlags = errors.BaseWrap(ff.ErrHelp, "missing required flags")
