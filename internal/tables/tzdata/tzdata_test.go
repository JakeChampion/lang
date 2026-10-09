package tzdata

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTableIsCurrent fails when the table checked into std/tz is not what
// the generator makes from the pinned release: a hand edit, a generator
// change that was not rerun, or a tzdata bump that was not regenerated.
func TestTableIsCurrent(t *testing.T) {
	path := filepath.Join("..", "..", "..", TzFern)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Generate(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(src) != want {
		t.Fatalf("%s is stale against tzdata %s: run `go run ./cmd/tzdatagen` from the repository root", TzFern, Version)
	}
}

// TestRecordsRoundTrip decodes every encoded record back to the zone it
// came from, the reading std/tz's decode_record mirrors.
func TestRecordsRoundTrip(t *testing.T) {
	files, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		z, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rec, err := z.Encode()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		back, err := Decode(rec)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := back.Encode()
		if err != nil || again != rec {
			t.Fatalf("%s: record does not round-trip:\n%s\n%s", name, rec, again)
		}
		if len(back.Times) != len(z.Times) || len(back.Types) != len(z.Types) {
			t.Fatalf("%s: decoded %d times / %d types, encoded %d / %d", name, len(back.Times), len(back.Types), len(z.Times), len(z.Types))
		}
		for i := range z.Times {
			if back.Times[i] != z.Times[i] || back.Idx[i] != z.Idx[i] {
				t.Fatalf("%s: transition %d decodes to %d/%d, want %d/%d", name, i, back.Times[i], back.Idx[i], z.Times[i], z.Idx[i])
			}
		}
	}
}
