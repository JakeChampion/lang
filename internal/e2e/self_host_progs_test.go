package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// selfHostProgCases are full self-contained programs compiled through
// the self-hosted compiler. They cover two pure-collection / self-host
// capabilities:
//
//   - map value-returning aliases (docs/PURE-COLLECTION-API-PLAN.md
//     §3a): `insert` ≡ `set`, recognised by the self-host front-end +
//     backends alongside the canonical name.
//   - tuple-destructure type inference: `var (a, b) = recv.method()`
//     now types each binding from the method's tuple return, so a
//     destructured struct/map element dispatches its own methods/fields
//     instead of mis-mangling as `__fn_i32__<m>`. Previously only
//     free-function tuple returns were typed; method-call returns fell
//     back to ("i32","i32").
//
// Exit codes cross-checked against the Go backend semantics.
var selfHostProgCases = []struct {
	name string
	src  string
	exit int
}{
	// Map.insert ≡ set, value-returning, with overwrite: {1:99, 2:20}.
	{"map-insert", `function main(): i32 { var m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); m = m.insert(1,99); return m.get_or(1,-1) + m.get_or(2,-1); }`, 119},
	{"map-insert-fresh", `function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(5,7); return m.get_or(5,0); }`, 7},

	// Tuple-destructure inference from a user method returning a tuple:
	// `q` must be typed Pair so `q.hi`/`q.lo` resolve (without the fix
	// `q` defaults to i32 and the struct-field access mis-compiles).
	// (3 + 7) + 7 = 17.
	{"destructure-method-tuple", `struct Pair { hi: i32, lo: i32 }
function (p: Pair) swapped(): (Pair, i32) { return (Pair { hi: p.lo, lo: p.hi }, p.hi); }
function main(): i32 { var p: Pair = Pair { hi: 7, lo: 3 }; var (q, old) = p.swapped(); return q.hi + q.lo + old; }`, 17},

	// NOTE: map `without` ≡ `delete` is recognised by the self-host
	// front-end (it compiles), and the tuple-destructure inference above
	// types its `(Map, bool)` result. It is not exercised here because
	// the *direct-asm* self-host backend has no map-delete emission yet
	// (a separate runtime gap, independent of this inference fix); the
	// `without`→`delete` path is covered on the Go backend by the
	// `pure_collection_aliases` fixture.
}

// TestSelfHostProgsX86_64 compiles each program with the self-hosted
// x86-64 compiler and checks the exit code.
func TestSelfHostProgsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range selfHostProgCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostProgsArm64 — CI-gated arm64 counterpart.
func TestSelfHostProgsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	for _, tc := range selfHostProgCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
