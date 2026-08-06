package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `Map { … }` literal carries its KEY KIND in the constructor the parser
// desugars it to — `map_new_i32` or `map_new` — and the chained `.insert`
// dispatch reads that back to pick the key compare. The parser used to decide
// it by matching the FIRST key against `ExprNumber` and nothing else, so every
// non-literal key fell through to the STRING constructor (#6207).
//
// Two or more computed keys SIGSEGV'd at construction: `__fern_map_set`
// compared the integer keys through `__fern_str_eq`, dereferencing them as
// pointers. Nothing had to read the map — building it was enough.
//
// Two fixes stack here. The syntactic one walks each key down to a decisive
// leaf (`(1 + 1)`, a cast's target) and takes the first key that answers.
// It cannot answer for a call, a field access or an `if` expression — the
// parser has no types — so the structural one reads the DECLARATION instead:
// `var m: Map[i32, i32] = …` retargets the desugared constructor outright,
// and the guess is only consulted where there is no annotation.
//
// `two_computed_keys` is the case that fails without the syntactic fix and
// `undecidable_keys_from_annotation` the one that fails without the
// structural fix (both verified by reverting the parser: they exit -1,
// killed by the signal). The rest pass either way and are here to pin the
// directions neither fix may break — a single insert never reaches the
// compare, and the string and un-annotated paths must keep the kind they
// had. Every case reads the map back rather than only constructing it, so a
// non-crashing mistyping would fail too. Oracle: the interpreter.
var mapLiteralComputedKeyCases = []struct {
	name string
	src  string
}{
	// The reduced repro's shape: two computed keys. 10 + 20 + 2 = 32.
	{"two_computed_keys", `import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map { (1i32 + 1i32): 10i32, (2i32 + 2i32): 20i32 };
    return m.get_or(2i32, 0i32) + m.get_or(4i32, 0i32) + m.len();
}`},
	// One computed key — the silent case. 7 + 1 = 8.
	{"one_computed_key", `import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map { (1i32 + 1i32): 7i32 };
    return m.get_or(2i32, 0i32) + m.len();
}`},
	// A computed key whose decisive literal is two nodes down, under the
	// out-of-range saturating shift the original generated seed used.
	// 296 <<| 403 masks to 296 << 19. 5 + 1 = 6.
	{"nested_shift_key", `import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map { (560i32 | ((372i32 & 865i32) <<| 422i32)): 5i32 };
    return m.get_or(23088i32, 0i32) + m.len();
}`},
	// The string direction must NOT flip: a computed STRING key stays
	// string-keyed. 5 + 1 = 6.
	{"computed_string_key", `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = Map { ("a" + "b"): 5i32 };
    return m.get_or("ab", 0i32) + m.len();
}`},
	// A cast is decisive on its TARGET even when nothing below it is: the
	// operand here is a call, which the parser cannot classify. Generated
	// programs really do key maps this way — this is the shape that survived
	// the first fix and kept seed 200 crashing. 7 + 3 + 2 = 12.
	{"cast_over_undecidable_operand", `import "core/map";
function k(): i32 { return 5i32; }
function j(): i32 { return 9i32; }
function main(): i32 {
    var m: Map[i32, i32] = Map { (k() as i32): 7i32, (j() as i32): 3i32 };
    return m.get_or(5i32, 0i32) + m.get_or(9i32, 0i32) + m.len();
}`},
	// The string direction of the ANNOTATION: a `Map[string, …]` declaration
	// with a key the guess also reads as string. Both agree; the retarget must
	// be a no-op rather than flipping it. 4 + 1 = 5.
	{"undecidable_key_string_annotation", `import "core/map";
function k(): string { return "z"; }
function main(): i32 {
    var m: Map[string, i32] = Map { k(): 4i32 };
    return m.get_or("z", 0i32) + m.len();
}`},
	// Keys the parser CANNOT classify at all — two calls. Nothing syntactic
	// decides these, so before the declaration was consulted this built
	// string-keyed and SIGSEGV'd on the second insert's compare. 6 + 4 + 2 = 12.
	{"undecidable_keys_from_annotation", `import "core/map";
function k(): i32 { return 3i32; }
function j(): i32 { return 8i32; }
function main(): i32 {
    var m: Map[i32, i32] = Map { k(): 6i32, j(): 4i32 };
    return m.get_or(3i32, 0i32) + m.get_or(8i32, 0i32) + m.len();
}`},
	// The reduced shape of seed 97: both keys are `if` expressions. An arm can
	// be decisive on its own but the `if` node is not an operand chain, so the
	// leaf walk stops at it — the declaration is the only thing that answers.
	// 9 + 5 + 2 = 16.
	{"if_expression_keys_from_annotation", `import "core/map";
function main(): i32 {
    var c: boolean = true;
    var m: Map[i32, i32] = Map {
        (if (c) { 11i32 } else { 12i32 }): 9i32,
        (if (c) { 21i32 } else { 22i32 }): 5i32
    };
    return m.get_or(11i32, 0i32) + m.get_or(21i32, 0i32) + m.len();
}`},
	// No annotation to read, so the syntactic guess is still what decides.
	// This is the direction the structural fix must not quietly take over.
	// 7 + 1 = 8.
	{"unannotated_keeps_the_guess", `import "core/map";
function main(): i32 {
    var m = Map { (1i32 + 1i32): 7i32 };
    return m.get_or(2i32, 0i32) + m.len();
}`},
}

// TestSelfHostMapLiteralComputedKeyIR_X86_64 pins the desugared constructor's
// key kind for map literals whose keys are expressions rather than bare
// literals (#6207), through the self-host IR path.
func TestSelfHostMapLiteralComputedKeyIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range mapLiteralComputedKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR path)", tc.name, got)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "maplitkey_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			cmd := exec.Command(argv[0], argv[1:]...)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle) — a map literal with a "+
					"computed key built itself with the wrong key kind", tc.name, code, want)
			}
		})
	}
}
