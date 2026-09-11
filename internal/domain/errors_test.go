package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorMessageHidesCause(t *testing.T) {
	cause := fmt.Errorf("SECRET-CAUSE")
	err := Wrap(cause, CodeResourceConflict, "conflict on %s", "job-1")
	if err.Error() != "conflict on job-1" {
		t.Fatalf("Error() = %q", err.Error())
	}
	if strings.Contains(err.Error(), "SECRET-CAUSE") {
		t.Fatalf("Error() leaked the internal cause: %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is should reach the wrapped cause")
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeResourceConflict {
		t.Fatalf("errors.As failed or wrong code: %+v", e)
	}
}

func TestErrorCodeFailClosed(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"plain error", errors.New("plain"), CodeInternalError},
		{"nil error", nil, CodeInternalError},
		{"empty code", &Error{Message: "x"}, CodeInternalError},
		{"domain error", NewError(CodeAuthUnauthorized, "no token"), CodeAuthUnauthorized},
		{"wrapped in std errors", fmt.Errorf("outer: %w", NewError(CodeRateLimited, "slow down")), CodeRateLimited},
	}
	for _, c := range cases {
		if got := ErrorCode(c.err); got != c.want {
			t.Fatalf("%s: ErrorCode = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short"); got != "short" {
		t.Fatalf("truncate(short) = %q", got)
	}
	long := strings.Repeat("a", 100)
	got := truncate(long)
	if len(got) != 67 || !strings.HasPrefix(got, strings.Repeat("a", 64)) {
		t.Fatalf("truncate(long) = %q (%d chars)", got, len(got))
	}
}
