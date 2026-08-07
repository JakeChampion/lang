package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostCheckerModloadX86_64 exercises the file-based checker driver
// (checker_modload_run.fern) — the import-driven successor to the
// checker_codes_bundle_run `///MODULE`-marker driver. It builds the driver,
// then points it at a 2-module program on disk: `main` imports `./a` and
// uses a.helper()'s i32 result as an `if` condition. The driver must
// resolve the import through the shared ./modloader, run the self-hosted
// checker over the merged module, print the diagnostic codes, and exit 1.
// The deliberate non-boolean condition is E008; cross-module resolution is
// what lets the checker know a.helper() is an i32 in the first place.
// buildCheckerModloadDriverX86 builds the file-based checker driver
// (checker_modload_run) as an x86 host binary — the import-driven successor
// to checker_codes_bundle_run. checker imports util/lexer/parser; the
// driver adds flatten + checker + modloader.
func buildCheckerModloadDriverX86(t *testing.T) (gcc string, runner []string, driverBin string) {
	t.Helper()
	gcc, runner = x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "lexer.fern", "parser.fern", "flatten.fern",
		"checker.fern", "modloader.fern", "fern_toml.fern", "checker_modload_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return gcc, runner, buildSelfHostBin(t, gcc, dir, "checker_modload_run.fern", "ckdriver")
}

// checkSourceModload resolves `entrySrc`'s full stdlib closure (real Go
// modload, like selfHostBundleFor), vendors each module as a flat
// <base>.fern next to main.fern + builtins.fern, runs the file-based
// checker driver, and returns its stdout (the diagnostic codes). The
// driver exits 1 when diagnostics are emitted, so the non-zero exit is
// tolerated.
func checkSourceModload(t *testing.T, runner []string, driverBin, entrySrc string) string {
	t.Helper()
	const entryPath = "/__fern_source__/main.fern"
	_, srcs, err := modload.LoadSource(entrySrc)
	if err != nil {
		t.Fatalf("modload.LoadSource: %v", err)
	}
	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	seen := map[string]string{}
	for p, src := range srcs {
		if p == entryPath {
			continue
		}
		b := strings.TrimSuffix(filepath.Base(p), ".fern")
		if prev, ok := seen[b]; ok {
			t.Fatalf("module-name collision: %q and %q both map to %q", prev, p, b)
		}
		seen[b] = p
		if err := os.WriteFile(filepath.Join(progDir, b+".fern"), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s.fern: %v", b, err)
		}
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"), []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, filepath.Join(progDir, "main.fern"))
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin, filepath.Join(progDir, "main.fern"))...)
	}
	out, _ := cmd.Output() // exit 1 on diagnostics — tolerate
	return string(out)
}

func TestSelfHostCheckerModloadX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	// A 2-module program with a deliberate E008 (non-boolean if condition).
	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "a.fern"),
		[]byte("pub function helper(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write a.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"),
		[]byte("import \"./a\";\nfunction main(): i32 { var x: i32 = a.helper(); if (x) { return 1; } return 0; }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}

	// Run the driver on the entry path. It exits 1 (diagnostics emitted),
	// so capture stdout without failing on the non-zero exit.
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, filepath.Join(progDir, "main.fern"))
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin, filepath.Join(progDir, "main.fern"))...)
	}
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "E008") {
		t.Errorf("checker driver: want an E008 diagnostic, got codes %q", string(out))
	}
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("checker driver exited %d, want 1 (diagnostics emitted)", code)
	}
}

// TestSelfHostCheckerModloadEmptyImplMangledX86_64 pins that an EMPTY
// `impl Trait for Type` block survives flatten's name mangling.
//
// flatten renames an imported module's decls (`Num` -> `num__Num`) and
// rewrites every type spelling with them, including each receiver method's
// receiver type — but it used to pass `mod.impls` through verbatim, so the
// impl table still said `Num` / `num.Num` while the providing method had
// become `num__Num`. The checker's E021 conformance scan resolves an empty
// impl by looking for an inherent method on `impl_type` ("empty impls adopt
// the existing method"), found nothing under the stale spelling, and
// reported every required method of every empty impl as missing. Real
// programs hit it through core/cmp's `impl Display for bigint.BigInt` et al,
// which any `import "std/array"` pulls in — four spurious E021s on a clean
// program. `flatten.mangle_impls` (#6398) fixed it; this is the pin, which
// that PR did not carry.
//
// Both shapes are covered: an empty impl on the impl-ing module's OWN struct
// (bare `Tag`, mangled by prefix) and one on a struct from another module
// (qualified `num.Num`, mangled by module map) — the bigint/cmp shape.
func TestSelfHostCheckerModloadEmptyImplMangledX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../examples/self_host/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	files := map[string]string{
		"builtins.fern": string(bsrc),
		// num: the struct + its inherent method, no impl block of its own.
		"num.fern": "pub struct Num { v: i32 }\n" +
			"pub function (n: Num) to_string(): string { return \"n\"; }\n",
		// cross: the trait, plus two empty impls — one on its OWN struct
		// (bare `Tag`) and one on num's (qualified `num.Num`).
		"cross.fern": "import \"./num\";\n" +
			"pub trait Show {\n    function to_string(self: Self): string;\n}\n" +
			"pub struct Tag { v: i32 }\n" +
			"pub function (t: Tag) to_string(): string { return \"t\"; }\n" +
			"impl Show for Tag { }\n" +
			"impl Show for num.Num { }\n",
		"main.fern": "import \"./num\";\nimport \"./cross\";\n" +
			"function main(): i32 { var n: num.Num = num.Num { v: 1 }; return n.v; }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, filepath.Join(progDir, "main.fern"))
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin, filepath.Join(progDir, "main.fern"))...)
	}
	out, _ := cmd.Output()
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("checker driver: want no diagnostics on a program whose empty impls are\nsatisfied by inherent methods, got codes %q", got)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("checker driver exited %d, want 0 (no diagnostics)", code)
	}
}
