package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// destructureFieldCases cover `var (a, b, c) = <source>` where the source is a
// struct FIELD, which `lower_stmt_var_destructure` had no arm for (#8458).
//
// It typed the destructured elements by matching `v.init` against ExprTuple /
// ExprIdent / ExprIndex / ExprCall. A field access matched none of them, fell
// through to the default, and every element read as an untyped 4 bytes — so an
// f64 element came back as its bit pattern (4612811918334231000) and an i64
// truncated (5000000001 → 705032705), on all three self-host backends, with no
// diagnostic. The identical destructure from a call, a local or a param was
// correct, which is what made it look like a tuple bug rather than a
// source-shape one.
//
// `tuple_decl_type` already resolved a field's declared tuple type — its own
// ExprFieldAccess arm exists for exactly this — so the information was there
// and unread. The fix asks it for anything the match leaves untyped, which
// covers a nested field and a receiver field by the same walk rather than
// adding one arm and waiting for the next shape to go missing.
//
// Each case returns a value the interp oracle also computes, so the assertion
// is the two agreeing rather than a hardcoded number.
var destructureFieldCases = []struct {
	name string
	src  string
}{
	// The gap: an f64 element destructured out of a struct field.
	{"destructure_field_f64", `struct P { t: (i32, f64) }
function main(): i32 {
    var p: P = P { t: (7, 2.5) };
    var (a, b) = p.t;
    return a + (b * 10.0) as i32;
}`},
	// The 8-byte integer sibling — pins that the WIDTH comes from the tag,
	// not only that the element exists.
	{"destructure_field_i64", `struct P { w: (i64, i32) }
function main(): i32 {
    var p: P = P { w: (5000000000, 7) };
    var (a, b) = p.w;
    return (a / 1000000000) as i32 + b;
}`},
	// A NESTED field source walks the same resolver.
	{"destructure_nested_field_f64", `struct Inner { t: (i32, f64) }
struct P { q: Inner }
function main(): i32 {
    var p: P = P { q: Inner { t: (9, 4.5) } };
    var (a, b) = p.q.t;
    return a + (b * 10.0) as i32;
}`},
	// A RECEIVER field inside a method — the shape a compiler pass writes.
	{"destructure_receiver_field_f64", `struct P { t: (i32, f64) }
function (p: P) sum(): i32 {
    var (a, b) = p.t;
    return a + (b * 10.0) as i32;
}
function main(): i32 { var p: P = P { t: (7, 2.5) }; return p.sum(); }`},
	// A string element off a field must still mark the slot a string.
	{"destructure_field_string", `struct P { t: (i32, string) }
function main(): i32 {
    var p: P = P { t: (7, "abcd") };
    var (a, b) = p.t;
    return a + b.len();
}`},
	// Negative guard: an all-i32 tuple field must stay 4-byte. A fix that
	// widened on any non-empty tag would break this.
	{"destructure_field_i32_narrow", `struct P { t: (i32, i32) }
function main(): i32 {
    var p: P = P { t: (30, 12) };
    var (a, b) = p.t;
    return a + b;
}`},
	// The shapes that were already correct, kept as controls: a fix that
	// disturbed the existing arms would show up here.
	{"destructure_call_f64", `function mk(): (i32, f64) { return (7, 2.5); }
function main(): i32 { var (a, b) = mk(); return a + (b * 10.0) as i32; }`},
	{"destructure_local_f64", `function main(): i32 {
    var t: (i32, f64) = (7, 2.5);
    var (a, b) = t;
    return a + (b * 10.0) as i32;
}`},
}

// TestSelfHostDestructureFieldIR_X86_64 runs each case through the self-host
// x86-64 IR path and compares against the interpreter.
func TestSelfHostDestructureFieldIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range destructureFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "destrfield_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
