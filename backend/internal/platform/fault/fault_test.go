package fault

import (
	"testing"

	"gitlab.com/tozd/go/errors"
)

func TestErrorFromHTTPCode(t *testing.T) {
	tests := []struct {
		code int
		want error
	}{
		{200, nil}, {204, nil},
		{400, ErrInvalidValue}, {401, ErrUnauthorized}, {403, ErrPermission}, {404, ErrNotFound},
		{408, ErrTimeout}, {429, ErrRateLimited}, {500, ErrTemporary}, {502, ErrTemporary}, {504, ErrTimeout},
	}
	for _, tt := range tests {
		got := ErrorFromHTTPCode(tt.code)
		if (tt.want == nil && got != nil) || (tt.want != nil && !errors.Is(got, tt.want)) {
			t.Errorf("ErrorFromHTTPCode(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}
