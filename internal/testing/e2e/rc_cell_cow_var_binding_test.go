package e2e

import (
	"strings"
	"testing"
)

var cellCowVarBindingCases = []struct {
	name string
	src  string
	want int
}{
	{"rebind", `import "core/map";
function main(): i32 {
  let m: Map[string, i32] = map_new(2);
  let f: () => i32 = (): i32 => { return m.len(); };
  let t: Map[string, i32] = m.insert("k", 1);
  m = t;
  return m.len() * 10 + f();
}`, 11},
	{"rebind_binding_read_after", `import "core/map";
function main(): i32 {
  let m: Map[string, i32] = map_new(2);
  let f: () => i32 = (): i32 => { return m.len(); };
  let t: Map[string, i32] = m.insert("k", 1);
  m = t;
  return m.len() * 10 + f() + t.len() * 100;
}`, 111},
	{"rebind_loop", `import "core/map";
function main(): i32 {
  let m: Map[string, i32] = map_new(2);
  let f: () => i32 = (): i32 => { return m.len(); };
  let i: i32 = 0;
  while (i < 50) {
    let t: Map[string, i32] = m.insert("k", i);
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
