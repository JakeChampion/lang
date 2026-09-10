package e2e

import (
	"strings"
	"testing"
)

// #8853 — `var t = m.insert(k, v); m = t` on a CAPTURED map. The map lives in
// a boxcapture cell, and a cow-in-place insert hands the cell's own element
// back borrowed, so the binding used to share the cell's one count and the
// store's release of the superseded element spent it: a correct 11 on both
// natives with `-sanitize` reporting an over-release. The binding now takes a
// count of its own exactly when the call returns the element it was given.
//
// Three shapes: the issue's, the binding read after the store (so its own
// release is exercised too), and a fifty-round rebinding loop, where the
// binding's previous value must be released on re-declaration or every
// round strands a map. The natives' exit code is checked against the
// interpreter's, the sanitizer must stay silent, and the census balanced.
var cellCowVarBindingCases = []struct {
	name string
	src  string
	want int
}{
	{"rebind", `import "core/map";
function main(): i32 {
  var m: Map[string, i32] = map_new(2);
  var f: () => i32 = (): i32 => { return m.len(); };
  var t: Map[string, i32] = m.insert("k", 1);
  m = t;
  return m.len() * 10 + f();
}`, 11},
	{"rebind_binding_read_after", `import "core/map";
function main(): i32 {
  var m: Map[string, i32] = map_new(2);
  var f: () => i32 = (): i32 => { return m.len(); };
  var t: Map[string, i32] = m.insert("k", 1);
  m = t;
  return m.len() * 10 + f() + t.len() * 100;
}`, 111},
	{"rebind_loop", `import "core/map";
function main(): i32 {
  var m: Map[string, i32] = map_new(2);
  var f: () => i32 = (): i32 => { return m.len(); };
  var i: i32 = 0;
  while (i < 50) {
    var t: Map[string, i32] = m.insert("k", i);
    m = t;
    i = i + 1;
  }
  return m.len() * 10 + f();
}`, 11},
}

func checkCellCowVarBinding(t *testing.T, stdout, stderr string, code, want int) {
	t.Helper()
	if code != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, want, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

func TestX86_64CellCowVarBindingOwnsItsCount(t *testing.T) {
	for _, tc := range cellCowVarBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Fatalf("interp exit = %d, want %d", got, tc.want)
			}
			stdout, stderr, code := runSanitizeX86_64(t, tc.src)
			checkCellCowVarBinding(t, stdout, stderr, code, tc.want)
		})
	}
}

func TestArm64CellCowVarBindingOwnsItsCount(t *testing.T) {
	for _, tc := range cellCowVarBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runSanitizeArm64(t, tc.src)
			checkCellCowVarBinding(t, stdout, stderr, code, tc.want)
		})
	}
}
