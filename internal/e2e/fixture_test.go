// Data-driven end-to-end fixtures. Each directory under
// `conformance/cases/<name>/` is a self-contained program that is
// compiled and run across every backend (interp, x86-64, arm64,
// wasm) and checked against its expected stdout and exit code. This
// is the declarative counterpart to the inline-source backend tests:
// to add a case you drop a `.fern` file plus a few sidecar files,
// no Go code required.
//
// Layout of a fixture directory:
//
//	main.fern        (required) entry point; `main(): i32`. May import
//	                 sibling `.fern` files (`import "./helper";`) and
//	                 stdlib modules.
//	*.fern           (optional) sibling modules pulled in by main.fern.
//	expected.stdout  (optional) expected stdout. Compared byte-for-byte
//	                 in the default "exact" mode; treated as a list of
//	                 required substrings (one per line) in "contains"
//	                 mode. Defaults to empty.
//	expected.exit    (optional) expected process exit code. Defaults to 0.
//	stdin            (optional) bytes fed to the program's stdin.
//	match            (optional) "exact" (default) or "contains".
//	backends         (optional) whitespace-separated subset of
//	                 {interp, x86_64, arm64, wasm}; lines starting with
//	                 '#' are comments. Defaults to all four.
//
// Compile-error fixtures: if a directory contains an `expected.error`
// file, the fixture is NOT run. Instead it is expected to FAIL the
// front-end (parse / module-load / type-check), and the captured error
// message must contain the trimmed `expected.error` text. This gives
// declarative coverage of the checker's rejection paths. Such fixtures
// ignore expected.stdout / expected.exit / stdin / match / backends.
//
// Backend exit-code note: native and interp backends propagate main's
// return value straight to the process exit code (full 0..255). A
// preview-2 wasm host only surfaces 0/1 through `wasi:cli/exit`, so we
// build the wasm component with PrintMainResult and recover main's
// value from the trailing result line (`<n>\n`) it appends to stdout —
// that gives exit-code parity on wasm too. Two consequences for the
// wasm leg: a fixture's `main` must return i32 (not void), and
// `int_to_string` must be reachable so the result line can be
// formatted — i.e. a wasm-targeting exact-match fixture must
// `import "core/int";`, or drop "wasm" from the fixture's
// `backends` file.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

type fixtureSpec struct {
	name         string
	mainPath     string
	stdin        string
	wantOut      string   // exact mode: full expected stdout
	contains     []string // contains mode: required substrings
	exact        bool     // true → byte-for-byte stdout match
	wantExit     int
	backends     map[string]bool
	compileError bool   // true → expected to fail the front-end
	wantError    string // required substring of the compile error
}

var allBackends = []string{"interp", "x86_64", "arm64", "wasm"}

func loadFixture(t *testing.T, dir string) *fixtureSpec {
	t.Helper()
	f := &fixtureSpec{
		name:     filepath.Base(dir),
		mainPath: filepath.Join(dir, "main.fern"),
		exact:    true,
		backends: map[string]bool{},
	}

	// Compile-error fixture: expected to fail the front-end. Skip all
	// the run-oriented sidecar parsing.
	if raw, ok := readOptionalFile(dir, "expected.error"); ok {
		f.compileError = true
		f.wantError = strings.TrimSpace(raw)
		if f.wantError == "" {
			t.Fatalf("%s: expected.error file is empty", f.name)
		}
		return f
	}

	if raw, ok := readOptionalFile(dir, "stdin"); ok {
		f.stdin = raw
	}

	mode := strings.TrimSpace(readOptionalFileDefault(dir, "match", "exact"))
	if mode == "contains" {
		f.exact = false
	} else if mode != "exact" {
		t.Fatalf("%s: unknown match mode %q (want \"exact\" or \"contains\")", f.name, mode)
	}

	stdout, _ := readOptionalFile(dir, "expected.stdout")
	if f.exact {
		f.wantOut = stdout
	} else {
		for _, ln := range strings.Split(stdout, "\n") {
			if s := strings.TrimSpace(ln); s != "" {
				f.contains = append(f.contains, s)
			}
		}
	}

	exitStr := strings.TrimSpace(readOptionalFileDefault(dir, "expected.exit", "0"))
	n, err := strconv.Atoi(exitStr)
	if err != nil {
		t.Fatalf("%s: bad expected.exit %q: %v", f.name, exitStr, err)
	}
	f.wantExit = n

	if raw, ok := readOptionalFile(dir, "backends"); ok {
		for _, ln := range strings.Split(raw, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, "#") {
				continue
			}
			for _, tok := range strings.Fields(ln) {
				f.backends[tok] = true
			}
		}
		for b := range f.backends {
			if !contains(allBackends, b) {
				t.Fatalf("%s: unknown backend %q in backends file", f.name, b)
			}
		}
	} else {
		for _, b := range allBackends {
			f.backends[b] = true
		}
	}
	return f
}

// TestFernFixtures discovers every directory under conformance/cases and
// runs it across the backends it opts into.
func TestFernFixtures(t *testing.T) {
	root := conformanceCases
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no fixtures found under %s", root)
	}

	for _, name := range names {
		dir, err := filepath.Abs(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("abs %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "main.fern")); err != nil {
			t.Errorf("fixture %s has no main.fern", name)
			continue
		}
		f := loadFixture(t, dir)
		t.Run(name, func(t *testing.T) {
			if f.compileError {
				t.Run("check", func(t *testing.T) {
					errText, failed := runFixtureCompileError(f.mainPath)
					if !failed {
						t.Errorf("expected a compile error containing %q, but the program compiled cleanly", f.wantError)
					} else if !strings.Contains(errText, f.wantError) {
						t.Errorf("compile error does not contain %q\nfull error:\n%s", f.wantError, errText)
					}
				})
				return
			}
			for _, backend := range allBackends {
				if !f.backends[backend] {
					continue
				}
				backend := backend
				t.Run(backend, func(t *testing.T) {
					stdout, exit := f.run(t, backend)
					f.check(t, backend, stdout, exit)
				})
			}
		})
	}
}

func (f *fixtureSpec) run(t *testing.T, backend string) (stdout string, exit int) {
	switch backend {
	case "interp":
		return runFixtureInterp(t, f.mainPath, f.stdin)
	case "x86_64":
		return runFixtureX86_64(t, f.mainPath, f.stdin)
	case "arm64":
		return runFixtureArm64(t, f.mainPath, f.stdin)
	case "wasm":
		return runFixtureWasm(t, f.mainPath, f.stdin)
	default:
		t.Fatalf("unknown backend %q", backend)
		return "", 0
	}
}

func (f *fixtureSpec) check(t *testing.T, backend, stdout string, exit int) {
	t.Helper()
	if f.exact {
		want := f.wantOut
		// wasm appends main's i32 result (+newline) to stdout; that
		// trailing line is how we read the exit code on a host that
		// only surfaces 0/1, so fold it into the expected stdout.
		if backend == "wasm" {
			want = f.wantOut + strconv.Itoa(f.wantExit) + "\n"
		}
		if stdout != want {
			t.Errorf("stdout mismatch\n got: %q\nwant: %q", stdout, want)
		}
	} else {
		for _, sub := range f.contains {
			if !strings.Contains(stdout, sub) {
				t.Errorf("stdout missing %q\nfull stdout:\n%s", sub, stdout)
			}
		}
	}

	if backend == "wasm" {
		// Exit value rides on stdout (handled above); a non-zero
		// wasmtime status here means the component trapped.
		if exit != 0 {
			t.Errorf("wasm component trapped (exit %d)", exit)
		}
		return
	}
	if exit != f.wantExit {
		t.Errorf("exit = %d, want %d\nstdout:\n%s", exit, f.wantExit, stdout)
	}
}

func runFixtureArm64(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
	info, prog := loadCheckMono(t, mainPath)
	asm, err := arm64codegen.Emit(prog, info)
	if err != nil {
		t.Fatalf("arm64 emit: %v", err)
	}
	bin := linkAsm(t, gcc, asm, "-static", "-nostdlib")
	cmd := runArm64Bin(qemu, bin)
	return runBin(cmd, stdin)
}

func runFixtureX86_64(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	info, prog := loadCheckMono(t, mainPath)
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("x86_64 emit: %v", err)
	}
	var bin string
	// FERN_NATIVE_ASM=1 routes the assemble+link step through the pure-Go
	// x86-64 native backend instead of gcc — used to audit native coverage
	// across the fixture suite. Default (unset) keeps the gcc path.
	if os.Getenv("FERN_NATIVE_ASM") != "" {
		text, rodata, aerr := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
		if aerr != nil {
			t.Fatalf("NATIVE-ASM-FAIL: %v", aerr)
		}
		bin = filepath.Join(t.TempDir(), "prog")
		if werr := os.WriteFile(bin, nativeelf.StaticExecutableDataX86(text, rodata), 0o755); werr != nil {
			t.Fatalf("write native bin: %v", werr)
		}
	} else {
		bin = linkAsm(t, gcc, asm, "-static", "-nostdlib", "-no-pie")
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	return runBin(cmd, stdin)
}

func runFixtureWasm(t *testing.T, mainPath, stdin string) (string, int) {
	t.Helper()
	skipIfPreview2Missing(t)
	info, prog := loadCheckMono(t, mainPath)
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
		PrintMainResult:    true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	component := finishComponentFromCoreBytes(t, core)
	so, _, ec := runComponent(t, component, runOpts{stdin: stdin})
	return so, ec
}

// runFixtureCompileError runs the front-end (modload → constfold →
// check) and returns the first error's text plus whether any stage
// failed. Backend-agnostic: parse / module-load / type errors are the
// same regardless of target, so this runs once per fixture.
func runFixtureCompileError(mainPath string) (string, bool) {
	prog, _, err := modload.Load(mainPath)
	if err != nil {
		return err.Error(), true
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err.Error(), true
	}
	if _, err := checker.Check(prog); err != nil {
		return err.Error(), true
	}
	return "", false
}

func linkAsm(t *testing.T, gcc, asm string, flags ...string) string {
	t.Helper()
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	args := append(append([]string{}, flags...), asmPath, "-o", binPath)
	if out, err := exec.Command(gcc, args...).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s\n--- asm ---\n%s", err, out, asm)
	}
	return binPath
}

func readOptionalFile(dir, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func readOptionalFileDefault(dir, name, def string) string {
	if s, ok := readOptionalFile(dir, name); ok {
		return s
	}
	return def
}
