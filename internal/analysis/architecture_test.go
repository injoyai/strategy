package analysis

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// analysisAllowlist is the closed import set for the analysis package.
// It enforces the §6.2 label-isolation rule structurally: analysis may
// never import the data stack (internal/data, internal/store,
// internal/pipeline, internal/server, screening, replay) — future
// returns can only arrive through the explicit LabelSet input. No HTTP,
// SQL, filesystem or reflection either: the package is pure
// computation. Extending this list is an architecture decision.
var analysisAllowlist = map[string]bool{
	"encoding/json": true,
	"math":          true,
	"sort":          true,
	"strconv":       true,
	"time":          true,

	"github.com/injoyai/strategy/internal/domain": true,
	"github.com/injoyai/strategy/internal/factor": true,
	"github.com/injoyai/strategy/internal/ports":  true,
}

// testOnlyAllowlist covers imports only test files may use.
var testOnlyAllowlist = map[string]bool{
	"errors":  true,
	"strings": true,
	"testing": true,
}

// guardFile inspects the package itself, so it is exempt from its own scan.
const guardFile = "architecture_test.go"

func TestAnalysisImportsAreAllowlisted(t *testing.T) {
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
			if analysisAllowlist[path] {
				continue
			}
			if isTest && testOnlyAllowlist[path] {
				continue
			}
			t.Errorf("%s imports %q which is not in the analysis allowlist (label isolation: labels enter only via the LabelSet input)", name, path)
		}
	}
}

// TestAnalysisDoesNotReadWallClock keeps the package pure and its
// artifacts deterministic: analysis never reads the wall clock.
func TestAnalysisDoesNotReadWallClock(t *testing.T) {
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
			t.Errorf("%s reads the wall clock directly; analysis must stay deterministic", name)
		}
	}
}
