package domain

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// domainAllowlist is the closed set of imports the domain package may use.
// Domain must never depend on HTTP, SQL or provider SDKs; extending this
// list is an architecture decision and belongs in an ADR.
var domainAllowlist = map[string]bool{
	"bytes":                         true,
	"encoding/json":                 true,
	"errors":                        true,
	"fmt":                           true,
	"regexp":                        true,
	"strings":                       true,
	"time":                          true,
	"time/tzdata":                   true,
	"unicode":                       true,
	"github.com/shopspring/decimal": true,
}

// testOnlyAllowlist covers imports only test files may use.
var testOnlyAllowlist = map[string]bool{
	"testing":   true,
	"math/rand": true,
}

// guardFile inspects the package itself, so it is exempt from its own scan.
const guardFile = "architecture_test.go"

func TestDomainImportsAreAllowlisted(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || name == guardFile {
			continue
		}
		isTest := strings.HasSuffix(name, "_test.go")
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import %s", name, imp.Path.Value)
			}
			if domainAllowlist[path] {
				continue
			}
			if isTest && testOnlyAllowlist[path] {
				continue
			}
			t.Errorf("%s imports %q which is not in the domain allowlist", name, path)
		}
	}
}

// TestDomainDoesNotReadWallClock keeps the package honest about time: the
// only legitimate time.Now caller lives in internal/ports (SystemClock).
func TestDomainDoesNotReadWallClock(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "time.Now(") {
			t.Errorf("%s reads the wall clock directly; inject ports.Clock instead", name)
		}
	}
}
