---
paths:
  - "backend/**/*.go"
---

# Errors: use gitlab.com/tozd/go/errors

All Go code in `backend/` uses `gitlab.com/tozd/go/errors` (imported as `errors`), which records stack traces and supports structured details.

- **Never** import the standard library `errors` package, and never use `fmt.Errorf`. tozd's `Is`, `As`, `Unwrap` and `Join` are drop-in proxies for the standard ones.
- **New errors:** `errors.New("...")`, or `errors.Errorf("...: %w", err)` to format or wrap. Both record a stack trace.
- **Sentinel errors** (package-level values for `errors.Is`): declare them with `errors.Base("...")`, which has no stack. Return them with a stack attached: `return errors.WithStack(ErrUnknownIntent)`, or `errors.WithDetails(...)`.
- **Errors from other packages** (stdlib, third-party libraries):
  - `errors.WithStack(err)` at the boundary, to keep the original error and add a stack.
  - `errors.WithMessage(err, "reading model")` to add context while keeping its message.
  - `errors.Wrap(err, "...")` to replace the message while keeping the cause.
- **Typed external errors:** map errors that callers need to handle from external libraries and services to Alfred's own typed errors in `backend/internal/platform/fault`, using `errors.BaseWrap(libErr, "...")`. Callers check them with `errors.Is` against `fault` values instead of depending on each library's error values. Keep `fault` even when it looks unused: it's a deliberate pattern meant to grow.
- **Custom error types** (e.g. `assistant.SpokenError`, `match.NoMatchError`) are fine. Return them wrapped with `errors.WithStack(&T{...})`, and check them with `errors.As`.
