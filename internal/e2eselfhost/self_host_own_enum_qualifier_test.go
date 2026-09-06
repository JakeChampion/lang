package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ownEnumQualifierLib is #8650's reproducer, widened to the shapes the one bug
// showed up in: a module that declares `enum E` and then spells its variants
// `E.Wrap` — in CONSTRUCTOR position with a constant payload (`E.Wrap(3)`) and
// with a variable one (`E.Wrap(n)`), as a payload-less VALUE (`E.Nil`), and in
// PATTERN position (`E.Wrap(k)` / `E.Nil`).
//
// Only an IMPORTED module was affected: flatten prefix-mangles a non-entry
// module's decls, so the enum becomes `lib__E` and its variants `lib__Wrap` /
// `lib__Nil`, but `E.Wrap` was only ever tested against the module-qualifier
// reading. Missing it left the constructor as a field access on a mangled enum
// NAME (bailing IR eligibility at `lib__probe` and taking the whole module to
// `module: AST`) and left the pattern spelled `E.Wrap` against an enum whose
// variants are `lib__Wrap` — so every variant read as uncovered and E030 fired
// on a match covering both.
//
// probe(5) = 1 (the E.Nil arm) + 3 (the constant payload) + 5 (the variable
// one) = 9, which is also what native returns for the same two files.
const ownEnumQualifierLib = `enum E { Wrap(i32), Nil }

pub function probe(n: i32): i32 {
    var a: E = E.Wrap(3);
    var b: E = E.Wrap(n);
    var u: E = E.Nil;
    var t: i32 = 0;
    match (u) {
        E.Wrap(k) => { t = t + k; },
        E.Nil => { t = t + 1; }
    }
    match (a) {
        E.Wrap(k) => { t = t + k; },
        E.Nil => { t = t + 100; }
    }
    match (b) {
        E.Wrap(k) => { t = t + k; },
        E.Nil => { t = t + 200; }
    }
    return t;
}
`

const ownEnumQualifierMain = `import "./lib";
function main(): i32 { return lib.probe(5); }
`

// TestSelfHostOwnEnumQualifierX86_64 pins #8650 end to end through the
// import-driven driver (asm_modload_run.fern), which is the only self-host
// entry point that resolves a real `import` graph and so the only one where
// the imported module gets prefix-mangled at all.
//
// Two assertions, because the bug had two faces and one resolution behind
// them. `-ir-probe` is the eligibility half: it reported
// `lib__probe: BAIL lower call const_func` and `module: AST`. Emitting and
// RUNNING the program is the other half plus the proof the arms still select:
// the checker rejected these files outright with E030, and a fix that merely
// stopped erroring while the qualified arm no longer matched would be worse
// than the bug.
func TestSelfHostOwnEnumQualifierX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	progDir := t.TempDir()
	for name, src := range map[string]string{
		"lib.fern":  ownEnumQualifierLib,
		"main.fern": ownEnumQualifierMain,
	} {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(progDir, "main.fern")

	t.Run("ir-probe", func(t *testing.T) {
		cmd := runX86_64Bin(runner, driverBin, entry, "-ir-probe")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run -ir-probe driver: %v", err)
		}
		report := string(out)
		for _, want := range []string{"lib__probe: ir", "main: ir", "module: IR"} {
			if !strings.Contains(report, want) {
				t.Errorf("probe report missing %q\n--- report ---\n%s", want, report)
			}
		}
	})

	t.Run("runs", func(t *testing.T) {
		progAsm := runDriverFile(t, runner, driverBin, entry)
		if len(progAsm) == 0 {
			t.Fatal("driver emitted 0 bytes")
		}
		progBin := buildBin(t, gcc, progDir, "prog", string(progAsm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
		}
		_, _ = cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 9 {
			t.Errorf("program exited %d, want 9", code)
		}
	})
}

// TestSelfHostOwnEnumQualifierCheckX86_64 is the same construct at the CHECKER
// (checker_modload_run.fern), which is where #8650's second face showed: the
// qualified arms of a match covering both variants each read as uncovered, so
// a correct match drew one E030 per variant.
//
// The mismatched-qualifier half is what proves the fix did not turn the checks
// reading the qualifier into no-ops. `Other.Text` on a `Kind` scrutinee is
// E029; before the fix the qualifier stayed spelled `Other` while the enum
// table held `lib__Other`, so the lookup missed and no qualifier diagnostic
// was reported at all.
func TestSelfHostOwnEnumQualifierCheckX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	check := func(t *testing.T, lib string) (string, int) {
		t.Helper()
		progDir := t.TempDir()
		bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
		if err != nil {
			t.Fatalf("read builtins.fern: %v", err)
		}
		files := map[string][]byte{
			"builtins.fern": bsrc,
			"lib.fern":      []byte(lib),
			"main.fern":     []byte(ownEnumQualifierMain),
		}
		for name, src := range files {
			if err := os.WriteFile(filepath.Join(progDir, name), src, 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		cmd := runX86_64Bin(runner, driverBin, filepath.Join(progDir, "main.fern"))
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	t.Run("clean", func(t *testing.T) {
		out, code := check(t, ownEnumQualifierLib)
		if code != 0 {
			t.Errorf("checker driver exited %d, want 0 (no diagnostics); codes %q", code, out)
		}
		if strings.Contains(out, "E030") {
			t.Errorf("exhaustive match over qualified arms reported E030: %q", out)
		}
	})

	t.Run("mismatched-qualifier", func(t *testing.T) {
		out, code := check(t, `enum Kind { Text, Number }
enum Other { Text, Blah }

pub function probe(n: i32): i32 {
    var k: Kind = Kind.Text;
    match (k) {
        Other.Text => { return 1; },
        Kind.Number => { return 2; }
    }
    return n;
}
`)
		if code != 1 {
			t.Errorf("checker driver exited %d, want 1 (diagnostics emitted); codes %q", code, out)
		}
		if !strings.Contains(out, "E029") {
			t.Errorf("want E029 for `Other.Text` on a Kind scrutinee, got codes %q", out)
		}
	})
}
