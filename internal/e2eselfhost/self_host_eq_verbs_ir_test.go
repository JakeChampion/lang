package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// eqVerbsIRCases exercise the Eq-driven generic array verbs
// (`contains` / `index_of` / `distinct`) through the self-host IR path
// on x86-64 + wasm. The single-program self-host driver resolves no
// imports, so the relevant surface is inlined from
// `internal/stdlib/std/array.fern`: the three `[T: Eq]` bodies, plus an
// inline `Eq` trait + the `i32` / `string` primitive impls (standing in
// for core/cmp, which the bare driver does not load — the same shape the
// derive-Eq IR cases use) and a `match`-based Option unwrap.
//
// The `Eq` BOUND is load-bearing here, not decoration: the self-host
// monomorphiser only clones BOUNDED generics. An unbounded `[T]` erases
// to a single pointer-compare clone, so `contains(["x","y","z"], "z")`
// would compare string-box identities and miss the match. With `[T: Eq]`
// the verbs clone per element type, so a `string` instance lowers its
// `==` to `str_eq` (byte equality) while an `i32` instance keeps the
// scalar compare. This pins, in one program: per-type monomorphisation
// of a `T[]`-consuming body, a nested bounded-generic call
// (`distinct` -> `contains`), an `Option[i32]` return through
// `Some`/`None`, and the string-vs-scalar `==` dispatch inside a
// monomorphised body. Each case returns a small deterministic int
// (<= 255), pinned to the `"ir"` path; expectations are oracle-checked
// against the native interpreter. FEATURE-AUDIT std/array row (#2689).
const eqVerbsIRPrelude = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
pub function contains[T: Eq](xs: T[], target: T): boolean {
    for x in xs {
        if (x == target) { return true; }
    }
    return false;
}
pub function index_of[T: Eq](xs: T[], target: T): Option[i32] {
    var i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i] == target) { return Some(i); }
        i = i + 1;
    }
    return None;
}
pub function distinct[T: Eq](xs: T[]): T[] {
    var out: T[] = [];
    for x in xs {
        if (!contains(out, x)) { out = out.append(x); }
    }
    return out;
}
function idx_or(o: Option[i32], d: i32): i32 {
    match (o) {
        Some(v) => { return v; },
        None => { return d; },
    }
    return d;
}
`

var eqVerbsIRCases = []struct {
	name string
	main string
	want int
}{
	// contains over i32[]: present (20) and absent (99).
	{"contains-i32", `var a: i32[] = [10, 20, 30, 20]; var r: i32 = 0; if (contains(a, 20)) { r = r + 1; } if (!contains(a, 99)) { r = r + 2; } return r;`, 3},
	// contains over string[]: primitive string `==`.
	{"contains-string", `var ss: string[] = ["x", "y", "z"]; var r: i32 = 0; if (contains(ss, "z")) { r = r + 4; } if (!contains(ss, "q")) { r = r + 8; } return r;`, 12},
	// index_of hit: 15 is at index 2.
	{"index-of-hit", `var a: i32[] = [5, 10, 15, 20]; return idx_or(index_of(a, 15), 0 - 1);`, 2},
	// index_of miss: returns the None default.
	{"index-of-miss", `var a: i32[] = [5, 10, 15, 20]; return idx_or(index_of(a, 99), 7);`, 7},
	// distinct i32[]: [3,1,3,2,1,3] -> [3,1,2]; len*30 + d0*5 + d1*3 + d2 (kept
	// < 126: wasmtime rejects a process exit code >= 126).
	{"distinct-i32", `var a: i32[] = [3, 1, 3, 2, 1, 3]; var d: i32[] = distinct(a); return d.len() * 30 + d[0] * 5 + d[1] * 3 + d[2];`, 110},
	// distinct string[]: [a,b,a,c,b] -> [a,b,c]; len = 3.
	{"distinct-string", `var ss: string[] = ["a", "b", "a", "c", "b"]; return distinct(ss).len();`, 3},
}

func eqVerbsIRSrc(mainBody string) string {
	return eqVerbsIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostArrayEqVerbsIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostArrayEqVerbsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range eqVerbsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(eqVerbsIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostArrayEqVerbsIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostArrayEqVerbsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host eq-verbs wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range eqVerbsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(eqVerbsIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "eq_verbs_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("eq-verbs wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
