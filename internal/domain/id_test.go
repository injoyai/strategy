package domain

import (
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	for _, s := range []string{"job-1", "a", "A9._-", strings.Repeat("x", MaxIDLength)} {
		if _, err := ParseID(s); err != nil {
			t.Fatalf("ParseID(%q) error: %v", s, err)
		}
	}
	for _, s := range []string{"", "-abc", ".abc", "a b", "a/b", "a\n", strings.Repeat("x", MaxIDLength+1)} {
		if _, err := ParseID(s); err == nil {
			t.Fatalf("ParseID(%q) should fail", s)
		} else if code := ErrorCode(err); code != CodeValidationInvalid {
			t.Fatalf("ParseID(%q) code = %s, want %s", s, code, CodeValidationInvalid)
		}
	}
}

func TestVersionRefValidate(t *testing.T) {
	good := VersionRef{ID: "job-1", Version: "v1"}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	for _, r := range []VersionRef{
		{ID: "", Version: "v1"},
		{ID: "job-1", Version: ""},
		{ID: "job-1", Version: "latest"},
		{ID: "job-1", Version: "Latest"},
		{ID: "job-1", Version: "v 1"},
	} {
		if err := r.Validate(); err == nil {
			t.Fatalf("Validate(%+v) should fail", r)
		} else if code := ErrorCode(err); code != CodeValidationInvalid {
			t.Fatalf("Validate(%+v) code = %s, want %s", r, code, CodeValidationInvalid)
		}
	}
}

func TestVersionRefMatch(t *testing.T) {
	ref := VersionRef{ID: "job-1", Version: "v1"}
	if !ref.Match(VersionRef{ID: "job-1", Version: "v1"}) {
		t.Fatal("identical refs should match")
	}
	for _, other := range []VersionRef{
		{ID: "job-1", Version: "v2"},
		{ID: "job-2", Version: "v1"},
	} {
		if ref.Match(other) {
			t.Fatalf("%+v should not match %+v", ref, other)
		}
	}
}
