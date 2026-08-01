package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// stringCases bundle the full std/string module (its imports are
// prelude-resident: core/int, std/i32, std/array) plus a main, and
// check the exit code. std/string links on the self-host once
// __memcpy + s.as_bytes() resolve (its bytes() body, which the
// self-host instead dispatches to __fern_str_bytes). Exit codes
// cross-checked vs the Go backend.
var stringCases = []struct {
	name string
	main string
	exit int
}{
	{"index-of", `return "hello world".index_of("world");`, 6},
	// Option-returning search family: find / rfind / find_ci wrap the
	// -1-sentinel primitives in Option, so "not found" is None. Match on
	// the result to recover the index (or a sentinel exit code).
	{"find-hit", `match ("hello world".find("world")) { Some(i) => { return i; }, None => { return 99; } } return 0;`, 6},
	{"find-miss", `match ("abc".find("zz")) { Some(_) => { return 1; }, None => { return 7; } } return 0;`, 7},
	{"rfind-hit", `match ("hello hello".rfind("hello")) { Some(i) => { return i; }, None => { return 99; } } return 0;`, 6},
	{"find-ci-hit", `match ("Hello World".find_ci("WORLD")) { Some(i) => { return i; }, None => { return 99; } } return 0;`, 6},
	{"trim-len", `return "  hi  ".trim().len();`, 2},
	{"to-upper", `var u: string = "abc".to_ascii_upper(); return u[0] as i32;`, 65},
	{"to-lower", `var u: string = "ABC".to_ascii_lower(); return u[0] as i32;`, 97},
	{"contains", `if ("hello".contains("ell")) { return 7; } return 0;`, 7},
	{"starts-with", `if ("hello".starts_with("he")) { return 7; } return 0;`, 7},
	{"replace-len", `return "a.b.c".replace(".", "-").len();`, 5},
	{"repeat-len", `return "ab".repeat(3).len();`, 6},
	{"split-count", `return "a,b,c".split(",").len();`, 3},
}

// stringImportSource is the well-formed spelling of the same program: IMPORT
// std/string rather than concatenating its source with a main.
//
// The concatenated form this replaces — read std/string.fern, append a main — had
// been ill-formed since std/unicode was added to std/string's imports; the
// header's "its imports are prelude-resident" went stale. `capitalize` calls
// `unicode.capitalize`, which a single-module compile cannot resolve, and the
// legacy AST emitter merely tolerated the dangling reference. Appending the
// dependency is not a fix either: std/unicode imports std/utf8, which imports
// std/string back, so the three are mutually recursive and only the module
// loader resolves them.
//
// So BOTH legs compile through asm_load_run (the loader driver) with the stdlib
// root, which is also how a real program uses std/string. Verified to route "ir".
func stringImportSource(mainBody string) []byte {
	return []byte("import \"std/string\";\nfunction main(): i32 { " + mainBody + " }\n")
}

// TestSelfHostStringX86_64 compiles a program IMPORTING std/string with the
// self-hosted x86-64 compiler and checks exit codes. It goes through the loader
// driver (asm_load_run) rather than the single-module one, because std/string is
// no longer self-contained — see stringImportSource.
func TestSelfHostStringX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "treeshake.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, stringImportSource(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
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

// TestSelfHostStringArm64 — CI-gated arm64 counterpart. Like the x86 leg it goes
// through the LOADER driver (asm_load_run -target arm64) rather than the
// single-module one, because std/string is not self-contained — see
// stringImportSource. The compiler runs as an x86 host binary emitting aarch64
// asm (the cross-compiler-on-host pattern), which the arm64 toolchain then
// assembles and qemu runs.
func TestSelfHostStringArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, _ := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "treeshake.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range stringCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, stringImportSource(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot, "-target", "arm64").Output()
			if cerr != nil {
				t.Fatalf("loader compile (arm64): %v", cerr)
			}
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
