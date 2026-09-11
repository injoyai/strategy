package ports

import (
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

func TestFixedClock(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFixedClock(base)
	if !c.Now().Equal(base) {
		t.Fatalf("Now() = %s, want %s", c.Now(), base)
	}
	c.Advance(90 * time.Minute)
	if !c.Now().Equal(base.Add(90 * time.Minute)) {
		t.Fatalf("After Advance: %s", c.Now())
	}
	c.Set(base.Add(-time.Hour))
	if !c.Now().Equal(base.Add(-time.Hour)) {
		t.Fatalf("After Set: %s", c.Now())
	}
}

func TestFixedClockNormalizesToUTC(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("system tzdata unavailable: %v", err)
	}
	c := NewFixedClock(time.Date(2024, 1, 1, 8, 0, 0, 0, sh))
	if c.Now().Location() != time.UTC {
		t.Fatalf("FixedClock location = %v, want UTC", c.Now().Location())
	}
	if !c.Now().Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("FixedClock Now = %s", c.Now())
	}
}

func TestSystemClockUTC(t *testing.T) {
	now := (SystemClock{}).Now()
	if now.Location() != time.UTC {
		t.Fatalf("SystemClock location = %v, want UTC", now.Location())
	}
}

func TestRandomIDGenerator(t *testing.T) {
	g := RandomIDGenerator{Prefix: "job"}
	seen := make(map[domain.ID]bool, 200)
	for i := 0; i < 200; i++ {
		id, err := g.NewID()
		if err != nil {
			t.Fatalf("NewID error: %v", err)
		}
		if !strings.HasPrefix(id.String(), "job_") {
			t.Fatalf("NewID = %q, want job_ prefix", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q after %d draws", id, i)
		}
		seen[id] = true
		if _, err := domain.ParseID(id.String()); err != nil {
			t.Fatalf("generated id %q fails ParseID: %v", id, err)
		}
	}
}

func TestSHA256Checksummer(t *testing.T) {
	got := (SHA256Checksummer{}).Checksum([]byte("abc"))
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("Checksum(abc) = %s, want %s", got, want)
	}
}
