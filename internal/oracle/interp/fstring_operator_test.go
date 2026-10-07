package interp

import (
	"bytes"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

// An f-string hole is evaluated through the checker's desugared chain, so the
// post-check lowerings of composite operators have to reach it: `a < b` on a
// struct ran as a raw struct comparison and failed (#11853).
func TestFStringHoleUsesOperatorMethods(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "std/string";
import "std/i32";
struct N { v: i32 }
function (a: N) cmp(b: N): i32 { if (a.v < b.v) { return -1; } if (a.v > b.v) { return 1; } return 0; }
function (a: N) eq(b: N): boolean { return a.v == b.v; }
function (a: N) add(b: N): N { return N { v: a.v + b.v }; }
function main(): i32 {
    let a: N = N { v: 1 };
    let b: N = N { v: 2 };
    print(f"{a < b} {a >= b} {a == b} {a != b} {(a + b).v}");
    return 0;
}`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	if _, err := i.CallByName("main", nil); err != nil {
		t.Fatalf("interp: %v", err)
	}
	if want := "true false false true 3\n"; buf.String() != want {
		t.Errorf("stdout = %q, want %q", buf.String(), want)
	}
}
