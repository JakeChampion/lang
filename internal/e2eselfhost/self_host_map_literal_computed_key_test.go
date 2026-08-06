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
// `two_computed_keys` is the case that fails without the fix (verified by
// reverting the parser: it exits -1, killed by the signal). The rest pass
// either way and are here to pin the directions the fix must not break —
// a single insert never reaches the compare, and the string and
// undecidable-key paths must keep the kind they had. Every case reads the
// map back rather than only constructing it, so a non-crashing mistyping
// would fail too. Oracle: the interpreter.
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
	// An undecidable key (a call) is the case the parser genuinely cannot
	// answer without types, so it still falls back to the string ctor. Pinned
	// as a STRING map so the fallback is exercised deliberately rather than
	// left to be discovered. 4 + 1 = 5.
	{"undecidable_key_falls_back", `import "core/map";
function k(): string { return "z"; }
function main(): i32 {
    var m: Map[string, i32] = Map { k(): 4i32 };
    return m.get_or("z", 0i32) + m.len();
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
