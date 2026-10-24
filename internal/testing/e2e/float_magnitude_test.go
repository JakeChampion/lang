package e2e

import (
	"strings"
	"testing"
)

func TestFloatScientificMagnitude(t *testing.T) {
	src := `import "std/f64";
function main(): i32 {
    write((1e-300).to_string()); write("|");
    write((1e300).to_string()); write("|");
    write((0.0).to_string()); write("|");
    write((3.5).to_string()); write("|");
    write((1e20).to_string()); write("|");
    write((0.000125).to_string()); write("\n");
    return 0;
}`
	out, code := compileAndRunX86_64(t, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\noutput: %q", code, out)
	}
	parts := strings.Split(strings.TrimRight(out, "\n"), "|")
	if len(parts) != 6 {
		t.Fatalf("got %d fields, want 6\noutput: %q", len(parts), out)
	}
	if parts[0] == "0" || !strings.Contains(parts[0], "e-3") {
		t.Errorf("1e-300 -> %q, want scientific with e-3xx (not silent 0)", parts[0])
	}
	if !strings.Contains(parts[1], "e+3") || strings.Contains(parts[1], "0000000000000000000") {
		t.Errorf("1e300 -> %q, want scientific with e+3xx (not an unmarked integer)", parts[1])
	}
	if parts[2] != "0" {
		t.Errorf("0.0 -> %q, want %q", parts[2], "0")
	}
	if parts[3] != "3.5" {
		t.Errorf("3.5 -> %q, want %q", parts[3], "3.5")
	}
	if parts[4] != "100000000000000000000" {
		t.Errorf("1e20 -> %q, want the plain integer (below the 1e21 sci threshold)", parts[4])
	}
	if parts[5] != "0.000125" {
		t.Errorf("0.000125 -> %q, want %q (E=-4, stays fixed-point)", parts[5], "0.000125")
	}
}
