---
paths:
  - "backend/**/*.go"
---

# Errors: tozd, with generic categories in `fault`

All Go code in `backend/` uses `gitlab.com/tozd/go/errors` (imported as `errors`), which records stack traces. Alfred's errors are a small set of **generic categories** defined in `backend/internal/platform/fault` (`ErrNotFound`, `ErrInvalidValue`, `ErrBadState`, `ErrDecoding`, `ErrTimeout`, `ErrTemporary`, `ErrServiceUnavailable`, `ErrInternal`, …), the same pattern as the greenfield backend.

- **Never** import the standard library `errors` package, and never use `fmt.Errorf`. tozd's `Is`, `As`, `Unwrap` and `Join` are drop-in proxies for the standard ones.
- **Raise a category with a message.** Don't create new error values:
  ```go
  return errors.WithMessagef(fault.ErrNotFound, "intent %q", name)
  return errors.WithMessage(fault.ErrBadState, "listener is already running")
  ```
- **Errors from other packages** (stdlib, third-party libraries):
  - Classify by prefixing a category, when callers need to handle the error: `errors.Prefix(err, fault.ErrServiceUnavailable)`.
  - Otherwise add context with `errors.WithMessage(err, "load whisper model")`, or `errors.WithStack(err)`.
- **Check categories, not packages:** `errors.Is(err, fault.ErrNotFound)`.
- **Package-specific errors only when very specific**, i.e. a type that carries data, like `match.NoMatchError{Best}`. Make it unwrap to a fault category (`func (e *NoMatchError) Unwrap() error { return fault.ErrNotFound }`), return it with `errors.WithStack(&T{...})`, and read it with `errors.As`. No package-level sentinel errors outside `fault`.
- **New category:** add it to `fault` only when a generic one doesn't fit. Prefer greenfield's names (`ErrExists`, `ErrConflict`, `ErrPermission`, `ErrRateLimited`, …).
- **Front ends map categories in one place:** `assistant.SpokenText` turns an error into speech (`SpokenError` text first, then category phrases). A future HTTP API gets an `HTTPCode` equivalent. Don't scatter category checks through handlers.
- Keep `fault` even when parts of it look unused: it's a deliberate pattern meant to grow.
