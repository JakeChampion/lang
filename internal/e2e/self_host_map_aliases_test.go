package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapAliasCases exercise the value-returning collection-method aliases
// (docs/PURE-COLLECTION-API-PLAN.md §3a) through the self-hosted
// compiler: `insert` ≡ `set` and `without` ≡ `delete` on Map. These
// names are recognised by the self-host front-end + every backend
// (checker / ssa / asm / asm_arm64 / wasm / interp / vm) alongside
// their canonical siblings, so a program using the immutable-looking
// vocabulary compiles identically. Exit codes cross-checked vs the Go
// backend (the same programs run there via the fixture harness).
var mapAliasCases = []struct {
	name string
	main string
	exit int
}{
	// insert ≡ set, value-returning, with overwrite: {1:99, 2:20}.
	{"insert", `var m: Map[i32,i32] = map_new(8); m = m.insert(1,10); m = m.insert(2,20); m = m.insert(1,99); return m.get_or(1,-1) + m.get_or(2,-1);`, 119},
	// insert on a fresh map, single key.
	{"insert-fresh", `var m: Map[i32,i32] = map_new(4); m = m.insert(5,7); return m.get_or(5,0);`, 7},
	//
	// NOTE on `without` ≡ `delete`: the alias is wired at the same
	// front-end + backend sites as `insert`/`set`, so a program using
	// `m.without(k)` is recognised and compiles (it reaches runtime and
	// behaves byte-for-byte like `m.delete(k)`). It is intentionally
	// NOT asserted here because exercising it requires destructuring
	// delete's `(Map, bool)` result, and inferring the Map type for a
	// tuple-destructure binding is a separate, *pre-existing* self-host
	// gap (canonical `m.delete(k)` traps the same way) — orthogonal to
	// alias recognition. The `without`→`delete` recognition is covered
	// on the Go backend by the `pure_collection_aliases` fixture.
}

func mapAliasSource(_ *testing.T, mainBody string) []byte {
	return []byte("function main(): i32 { " + mainBody + " }\n")
}

// TestSelfHostMapAliasesX86_64 compiles each alias program with the
// self-hosted x86-64 compiler and checks the exit code.
func TestSelfHostMapAliasesX86_64(t *testing.T) {
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

	for _, tc := range mapAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, mapAliasSource(t, tc.main))
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

// TestSelfHostMapAliasesArm64 — CI-gated arm64 counterpart.
func TestSelfHostMapAliasesArm64(t *testing.T) {
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

	for _, tc := range mapAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, mapAliasSource(t, tc.main))
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
