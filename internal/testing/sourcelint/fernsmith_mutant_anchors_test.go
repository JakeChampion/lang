package sourcelint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Only the nightly lane runs scripts/fernsmith-mutant. This test finds a
// changed anchor in compiler/lift.fern before that lane fails.
func TestFernsmithMutantAnchorsExist(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "scripts", "fernsmith-mutant-plants.json"))
	if err != nil {
		t.Fatalf("read fernsmith-mutant-plants.json: %v", err)
	}
	var plants []struct {
		Where  string `json:"where"`
		Anchor string `json:"anchor"`
		Plant  string `json:"plant"`
	}
	if err := json.Unmarshal(b, &plants); err != nil {
		t.Fatalf("parse fernsmith-mutant-plants.json: %v", err)
	}
	if len(plants) == 0 {
		t.Fatal("fernsmith-mutant-plants.json has no plants")
	}
	src, err := os.ReadFile(filepath.Join(root, "compiler", "lift.fern"))
	if err != nil {
		t.Fatalf("read compiler/lift.fern: %v", err)
	}
	for _, p := range plants {
		if p.Where == "" || p.Anchor == "" || p.Plant == "" {
			t.Errorf("plant %q has an empty field", p.Where)
			continue
		}
		if n := strings.Count(string(src), p.Anchor); n != 1 {
			t.Errorf("the anchor for %s occurs %d times in compiler/lift.fern, not once: update scripts/fernsmith-mutant-plants.json\nanchor:\n%s", p.Where, n, p.Anchor)
		}
	}
}
