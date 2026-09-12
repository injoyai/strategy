package synthetic

import (
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

func countSeverity(issues []Issue, severity string) int {
	n := 0
	for _, i := range issues {
		if i.Severity == severity {
			n++
		}
	}
	return n
}

func countCode(issues []Issue, code string) int {
	n := 0
	for _, i := range issues {
		if i.Code == code {
			n++
		}
	}
	return n
}

func TestQualityCleanFixture(t *testing.T) {
	engine := NewQualityEngine(fixtureCalendar, testNow)
	issues := engine.Check(fixtureBars)
	if len(issues) != 2 {
		t.Fatalf("issues = %d (%v), want 2", len(issues), issues)
	}
	if countCode(issues, domain.CodeQualityMissingTradingDay) != 2 {
		t.Fatalf("missing_trading_day count = %d, want 2", countCode(issues, domain.CodeQualityMissingTradingDay))
	}
	for _, i := range issues {
		if i.Severity != domain.SeverityWarning {
			t.Fatalf("issue %s severity = %q, want warning", i.Code, i.Severity)
		}
	}
	paths := make(map[string]bool)
	for _, i := range issues {
		paths[i.Path] = true
	}
	if !paths["instrument=INST_B/day=2026-01-08"] || !paths["instrument=INST_B/day=2026-01-09"] {
		t.Fatalf("missing day paths = %v", paths)
	}
	if !CanPublish(issues) {
		t.Fatal("clean fixture with warnings must be publishable")
	}
}

func TestQualityBadFixtureCounts(t *testing.T) {
	all := make([]domain.Observation, 0, len(fixtureBars)+len(fixtureBadBars))
	all = append(all, fixtureBars...)
	all = append(all, fixtureBadBars...)
	engine := NewQualityEngine(fixtureCalendar, testNow)
	issues := engine.Check(all)
	if len(issues) != 12 {
		t.Fatalf("issues = %d (%v), want 12", len(issues), issues)
	}
	if countSeverity(issues, domain.SeverityError) != 9 {
		t.Fatalf("errors = %d, want 9", countSeverity(issues, domain.SeverityError))
	}
	if countSeverity(issues, domain.SeverityInfo) != 1 {
		t.Fatalf("infos = %d, want 1", countSeverity(issues, domain.SeverityInfo))
	}
	if countSeverity(issues, domain.SeverityWarning) != 2 {
		t.Fatalf("warnings = %d, want 2", countSeverity(issues, domain.SeverityWarning))
	}
	wantCodes := map[string]int{
		domain.CodeQualityDuplicateNaturalKey: 4,
		domain.CodeQualityOHLCViolation:       2,
		domain.CodeQualityNegativeVolume:      1,
		domain.CodeQualityUnknownUnit:         1,
		domain.CodeQualityFutureAvailableAt:   1,
		domain.CodeQualitySupersededRevision:  1,
		domain.CodeQualityMissingTradingDay:   2,
	}
	for code, want := range wantCodes {
		if got := countCode(issues, code); got != want {
			t.Fatalf("code %s count = %d, want %d", code, got, want)
		}
	}
	if CanPublish(issues) {
		t.Fatal("issues with errors must block publishing")
	}
}

func TestQualityHighBelowLow(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	obs := barObs("INST_A", "2026-01-05", "10.00", "9.50", "10.00", "9.50", "1000")
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if issues[0].Code != domain.CodeQualityOHLCViolation {
		t.Fatalf("code = %q", issues[0].Code)
	}
	if !strings.Contains(issues[0].Message, "high (9.5) < low (10)") {
		t.Fatalf("message = %q", issues[0].Message)
	}
	if issues[0].Severity != domain.SeverityError {
		t.Fatalf("severity = %q", issues[0].Severity)
	}
}

func TestQualityOpenBelowLow(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	obs := barObs("INST_A", "2026-01-05", "9.00", "11.00", "10.00", "10.50", "1000")
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if !strings.Contains(issues[0].Message, "open (9) < low (10)") {
		t.Fatalf("message = %q", issues[0].Message)
	}
}

func TestQualityCloseAboveHigh(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	obs := barObs("INST_A", "2026-01-05", "10.00", "11.00", "9.90", "12.00", "1000")
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if !strings.Contains(issues[0].Message, "close (12) > high (11)") {
		t.Fatalf("message = %q", issues[0].Message)
	}
}

func TestQualityNegativeVolume(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	obs := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "-100")
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if issues[0].Code != domain.CodeQualityNegativeVolume {
		t.Fatalf("code = %q", issues[0].Code)
	}
	if !strings.Contains(issues[0].Message, "negative volume -100") {
		t.Fatalf("message = %q", issues[0].Message)
	}
}

func TestQualityUnknownUnit(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	obs := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000")
	obs.Values["unit"] = domain.Value{Kind: domain.ValueString, Encoded: "XXX"}
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if issues[0].Code != domain.CodeQualityUnknownUnit {
		t.Fatalf("code = %q", issues[0].Code)
	}
	if !strings.Contains(issues[0].Message, `unknown unit "XXX"`) {
		t.Fatalf("message = %q", issues[0].Message)
	}
}

func TestQualityFutureAvailableAt(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	obs := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000")
	obs.Provenance.AvailableAt = testNow.Add(time.Hour)
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if issues[0].Code != domain.CodeQualityFutureAvailableAt {
		t.Fatalf("code = %q", issues[0].Code)
	}
	if !strings.Contains(issues[0].Message, "is after the ingestion time") {
		t.Fatalf("message = %q", issues[0].Message)
	}
}

func TestQualityMissingTradingDays(t *testing.T) {
	engine := NewQualityEngine(fixtureCalendar, testNow)
	obs := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000")
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 4 {
		t.Fatalf("issues = %d (%v), want 4", len(issues), issues)
	}
	for _, i := range issues {
		if i.Code != domain.CodeQualityMissingTradingDay {
			t.Fatalf("code = %q, want missing_trading_day", i.Code)
		}
		if !strings.HasPrefix(i.Path, "instrument=INST_A/day=") {
			t.Fatalf("path = %q", i.Path)
		}
	}
	if !CanPublish(issues) {
		t.Fatal("missing days are warnings and must not block publishing")
	}
}

func TestQualityDuplicateNaturalKey(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	a := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000")
	b := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000")
	issues := engine.Check([]domain.Observation{a, b})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if issues[0].Code != domain.CodeQualityDuplicateNaturalKey {
		t.Fatalf("code = %q", issues[0].Code)
	}
	if !strings.Contains(issues[0].Message, "appears 2 times") {
		t.Fatalf("message = %q", issues[0].Message)
	}
	if !strings.Contains(issues[0].Message, "revision=rev-001") {
		t.Fatalf("message = %q", issues[0].Message)
	}
	if CanPublish(issues) {
		t.Fatal("duplicate natural key must block publishing")
	}
}

func TestQualitySupersededRevision(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	a := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000")
	b := barObs("INST_A", "2026-01-05", "10.00", "10.60", "9.90", "10.50", "1050")
	b.Provenance.RevisionID = "rev-002"
	issues := engine.Check([]domain.Observation{a, b})
	if len(issues) != 1 {
		t.Fatalf("issues = %d (%v), want 1", len(issues), issues)
	}
	if issues[0].Code != domain.CodeQualitySupersededRevision {
		t.Fatalf("code = %q", issues[0].Code)
	}
	if issues[0].Severity != domain.SeverityInfo {
		t.Fatalf("severity = %q, want info", issues[0].Severity)
	}
	if countCode(issues, domain.CodeQualityDuplicateNaturalKey) != 0 {
		t.Fatal("distinct revisions must not count as duplicates")
	}
	if !CanPublish(issues) {
		t.Fatal("revision supersession is info and must not block publishing")
	}
}

func TestQualityNilInstrumentRow(t *testing.T) {
	engine := NewQualityEngine(nil, testNow)
	obs := barObs("INST_A", "2026-01-05", "10.00", "10.50", "9.90", "10.40", "1000")
	obs.InstrumentID = nil
	issues := engine.Check([]domain.Observation{obs})
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestCanPublishMatrix(t *testing.T) {
	if !CanPublish(nil) {
		t.Fatal("nil issues must be publishable")
	}
	warning := Issue{Code: domain.CodeQualityMissingTradingDay, Severity: domain.SeverityWarning}
	info := Issue{Code: domain.CodeQualitySupersededRevision, Severity: domain.SeverityInfo}
	blocker := Issue{Code: domain.CodeQualityOHLCViolation, Severity: domain.SeverityError}
	if !CanPublish([]Issue{warning}) || !CanPublish([]Issue{info}) {
		t.Fatal("warning/info issues must be publishable")
	}
	if CanPublish([]Issue{blocker}) {
		t.Fatal("error issue must block publishing")
	}
	if CanPublish([]Issue{blocker, warning}) {
		t.Fatal("error issue must block publishing even with warnings")
	}
}
