package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const shippedArgsSrc = `function main(): i32 {
  let a: string[] = args();
  let i: i32 = 1;
  while (i < a.len()) {
    print(a[i]);
    i = i + 1;
  }
  return a.len();
}
`

// runFern runs bin with args under a private cache directory and returns its
// combined output and exit status.
func runFern(t *testing.T, bin, cache string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = e2eharness.ChildEnv("FERN_CACHE_DIR=" + cache)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func writeShippedSrc(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The shipped `fern` carries its stdlib: it needs no stdlib root on the
// command line, so every positional past the source file is the program's,
// and it writes the stdlib to the cache directory once.
func TestSelfHostShippedFernCarriesItsStdlib(t *testing.T) {
	fern := e2eharness.ShippedFern(t)
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	prog := writeShippedSrc(t, dir, "args.fern", shippedArgsSrc)
	stdProg := writeShippedSrc(t, dir, "std.fern", "import \"std/path\";\nfunction main(): i32 { print(path.path_file_name(\"/a/b.fern\")); return 0; }\n")

	if out, code := runFern(t, fern, cache, "-interp", prog, "one", "-two"); code != 3 || out != "one\n-two\n" {
		t.Errorf("-interp with program arguments: exit %d, output %q; want 3, \"one\\n-two\\n\"", code, out)
	}
	if out, code := runFern(t, fern, cache, "-interp", stdProg); code != 0 || out != "b.fern\n" {
		t.Errorf("-interp importing std/path: exit %d, output %q", code, out)
	}
	if out, code := runFern(t, fern, cache, "-run", "-target", e2eharness.HostSelfHostTarget(), prog, "x"); code != 2 || out != "x\n" {
		t.Errorf("-run: exit %d, output %q; want 2, \"x\\n\"", code, out)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	var stdlibs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "stdlib-") {
			stdlibs = append(stdlibs, e.Name())
		}
	}
	if len(stdlibs) != 1 || strings.Contains(stdlibs[0], ".tmp-") {
		t.Errorf("cache holds %v, want one stdlib-<digest> directory and no leftover temporary", stdlibs)
	}
}

// -version names the commit the embedded VERSION records, and -explain reads
// the explanations the binary carries.
func TestSelfHostShippedFernVersionAndExplain(t *testing.T) {
	fern := e2eharness.ShippedFern(t)
	cache := filepath.Join(t.TempDir(), "cache")
	out, code := runFern(t, fern, cache, "-version")
	if code != 0 || !strings.HasPrefix(out, "fern ") || strings.Contains(out, "unknown build") || !strings.Contains(out, "\nbuilt for ") {
		t.Errorf("-version: exit %d, output %q", code, out)
	}
	out, code = runFern(t, fern, cache, "-explain", "e003")
	if code != 0 || !strings.HasPrefix(out, "error E003: Cannot assign value to variable of different type\n\n") || strings.Contains(out, "# E003") {
		t.Errorf("-explain e003: exit %d, output %q; want the page's heading once, as the header", code, out)
	}
	out, code = runFern(t, fern, cache, "-explain")
	if code != 0 || !strings.Contains(out, "\nE003\n") || !strings.HasPrefix(out, "E001\n") {
		t.Errorf("-explain listing: exit %d, output %q", code, out)
	}
	out, code = runFern(t, fern, cache, "-explain", "E999")
	if code != 1 || !strings.Contains(out, `unknown error code "E999"`) {
		t.Errorf("-explain E999: exit %d, output %q", code, out)
	}
}

// The self-host CLI's -run executes what it built with the program's
// arguments, under the emulator for a foreign ISA, and removes the temporary
// binary.
func TestSelfHostCLIRun(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	prog := writeShippedSrc(t, dir, "args.fern", shippedArgsSrc)
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	// Each Linux target this host runs, natively or under its emulator.
	var targets []string
	for _, c := range []struct{ target, qemu string }{{e2eharness.TargetX86_64Linux, "qemu-x86_64"}, {e2eharness.TargetArm64Linux, "qemu-aarch64"}} {
		if _, err := exec.LookPath(c.qemu); err == nil || c.target == e2eharness.HostSelfHostTarget() {
			targets = append(targets, c.target)
		}
	}
	for _, target := range targets {
		cmd := exec.Command(cli, "-run", "-target", target, prog, root, "a", "b")
		cmd.Env = e2eharness.ChildEnv("TMPDIR=" + tmp)
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 3 || string(out) != "a\nb\n" {
			t.Errorf("%s: -run exit %d, output %q; want 3, \"a\\nb\\n\"", target, code, out)
		}
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("-run left %d entries in TMPDIR", len(left))
	}
	cmd := exec.Command(cli, "-run", "-emit", "asm", "-target", "x86-64-linux", prog, root)
	if out, _ := cmd.CombinedOutput(); cmd.ProcessState.ExitCode() != 2 {
		t.Errorf("-run -emit asm: exit %d, want the usage refusal 2 (%s)", cmd.ProcessState.ExitCode(), out)
	}
}

// -cover-report folds a -cover run's stream into the per-file summary and the
// lcov tracefile; a malformed prefixed row is an error, not a skip.
func TestSelfHostCLICoverReport(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	dir := t.TempDir()
	stream := strings.Join([]string{
		"program noise",
		"fern-cover: a.fern:2 1",
		"fern-cover: a.fern:3 0",
		"fern-cover: a.fern:4 0",
		"fern-cover: a.fern:5 0",
		"fern-branch: a.fern:2:7 E 4",
		"fern-branch: a.fern:2:7 T 4",
		"fern-cover: b.fern:1 2",
		"fern-cover: a.fern:2 1",
		"",
	}, "\n")
	in := writeShippedSrc(t, dir, "cover.txt", stream)
	want := "a.fern: 1/4 lines (25.0%), 1/2 branches (50.0%)\n" +
		"  uncovered lines: 3-5\n" +
		"  uncovered branches: 2:7 false\n" +
		"b.fern: 1/1 lines (100.0%)\n" +
		"total: 2/5 lines (40.0%), 1/2 branches (50.0%)\n"
	if out, err := exec.Command(cli, "-cover-report", in).CombinedOutput(); err != nil || string(out) != want {
		t.Errorf("summary: %v\n--- got ---\n%s--- want ---\n%s", err, out, want)
	}
	wantLcov := "SF:a.fern\nDA:2,2\nDA:3,0\nDA:4,0\nDA:5,0\nBRDA:2,0,0,0\nBRDA:2,0,1,4\nLF:4\nLH:1\nBRF:2\nBRH:1\nend_of_record\n" +
		"SF:b.fern\nDA:1,2\nLF:1\nLH:1\nend_of_record\n"
	if out, err := exec.Command(cli, "-cover-report", "-lcov", in).CombinedOutput(); err != nil || string(out) != wantLcov {
		t.Errorf("lcov: %v\n--- got ---\n%s--- want ---\n%s", err, out, wantLcov)
	}
	bad := writeShippedSrc(t, dir, "bad.txt", "fern-cover: a.fern:x 1\n")
	cmd := exec.Command(cli, "-cover-report", bad)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() != 1 || !strings.Contains(string(out), "line 1:") {
		t.Errorf("malformed row: exit %d, output %q", cmd.ProcessState.ExitCode(), out)
	}
}

// A build with no embedded tree has no VERSION, and says so. It reads the
// explanations of the source tree its stdlib root names, and without that root
// has none.
func TestSelfHostCLIWithoutEmbeddedTree(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	if out, _ := exec.Command(cli, "-version").CombinedOutput(); !strings.HasPrefix(string(out), "fern (unknown build)\n") {
		t.Errorf("-version = %q", out)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command(cli, args...)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}
	if out, code := run("-explain", "E003"); code != 2 || !strings.Contains(out, "carries no explanations: pass the stdlib root") {
		t.Errorf("-explain E003: exit %d, output %q", code, out)
	}
	if out, code := run("-explain", "e003", root); code != 0 || !strings.HasPrefix(out, "error E003: Cannot assign value to variable of different type\n\n") || strings.Contains(out, "# E003") {
		t.Errorf("-explain e003 %s: exit %d, output %q; want the page's heading once, as the header", root, code, out)
	}
	if out, code := run("-explain=", root); code != 0 || !strings.HasPrefix(out, "E001\n") || !strings.Contains(out, "\nE003\n") {
		t.Errorf("-explain= %s: exit %d, output %q; want the list of codes", root, code, out)
	}
	if out, code := run("-explain", "E003", root, "extra"); code != 2 {
		t.Errorf("-explain E003 %s extra: exit %d, output %q; want the usage refusal 2", root, code, out)
	}
}

// A positioned diagnostic shows its source line and a caret under the column
// (tabs shown as one space), as native's renderer does; -color draws a
// line-number gutter, box-drawing under a UTF-8 locale and `|` under -ascii.
func TestSelfHostCLIDiagnosticSnippet(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	src := writeShippedSrc(t, t.TempDir(), "bad.fern", "function main(): i32 {\n\tlet x: i32 = \"hi\";\n  return x;\n}\n")
	run := func(env []string, args ...string) string {
		cmd := exec.Command(cli, append(args, src, root)...)
		cmd.Env = e2eharness.ChildEnv(env...)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	want := src + ":2:2: error[E003]: variable \"x\": expected i32, got string\n" +
		"     let x: i32 = \"hi\";\n" +
		"     ^\n"
	if got := run(nil, "-check"); got != want {
		t.Errorf("plain:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
	wantColor := src + ":2:2: \x1b[1;31merror[E003]\x1b[0m: variable \"x\": expected i32, got string\n" +
		" 2 \x1b[34m│\x1b[0m  let x: i32 = \"hi\";\n" +
		"   \x1b[34m│\x1b[0m  \x1b[31m^\x1b[0m\n"
	if got := run([]string{"LANG=en_US.UTF-8"}, "-check", "-color", "always"); got != wantColor {
		t.Errorf("colour:\n--- got ---\n%q\n--- want ---\n%q", got, wantColor)
	}
	if got := run([]string{"LANG=en_US.UTF-8"}, "-check", "-color", "always", "-ascii"); !strings.Contains(got, " 2 \x1b[34m|\x1b[0m ") {
		t.Errorf("-ascii gutter: %q", got)
	}
	if got := run([]string{"NO_COLOR=1"}, "-check", "-color", "auto"); got != want {
		t.Errorf("NO_COLOR: %q", got)
	}
}

// -fmt reprints a `todo` stub, with or without its message, and a boolean
// match whose `false` arm closes the domain, as written: neither the loop a
// `todo` lowers to nor the `_` the closing arm is treated as.
func TestSelfHostFmtKeepsTodoAndClosedBooleanArms(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	src := "function g(n: i32): i32 {\n  todo;\n}\n\n" +
		"function h(): i32 {\n  todo(\"later\");\n}\n\n" +
		"function pick(b: boolean): i32 {\n  match (b) {\n    true => {\n      return 7;\n    },\n    false => {\n      return 3;\n    }\n  }\n}\n\n" +
		"function name(b: boolean): string {\n  return match (b) { false => \"no\", true => \"yes\" };\n}\n"
	path := writeShippedSrc(t, t.TempDir(), "t.fern", src)
	out, err := exec.Command(cli, "-fmt", path).CombinedOutput()
	if err != nil || string(out) != src {
		t.Errorf("-fmt: %v\n--- got ---\n%s--- want ---\n%s", err, out, src)
	}
}

// Without -o, a compile prints its assembly for arm64-linux, the target a
// compile gets when none is named, and for every other native target.
func TestSelfHostCLIWritesAssemblyToStdout(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	src := writeShippedSrc(t, t.TempDir(), "main.fern", shippedArgsSrc)
	out, err := exec.Command(cli, src, root).Output()
	if err != nil || !strings.Contains(string(out), "__fn_main:\n") || !strings.Contains(string(out), "stp x29, x30, [sp, #-16]!") {
		t.Errorf("no -o, no -target: %v\n%.400s", err, out)
	}
	for _, target := range []string{"x86-64-linux", "arm64-darwin", "arm64-android"} {
		out, err := exec.Command(cli, "-target", target, src, root).Output()
		if err != nil || !strings.Contains(string(out), ".text") || strings.ContainsRune(string(out), 0) {
			t.Errorf("no -o, -target %s: %v, want assembly text\n%.200q", target, err, out)
		}
	}
}

// Stdout takes only a target's text, so a wasm component or module with no -o
// is a usage error, exit 2, with nothing written. It is refused before the
// program is read: this one declares no HTTP handler, and the refusal still
// comes first. `-emit asm` is the WAT, which stdout takes, and -check writes
// nothing at all.
func TestSelfHostCLIRefusesWasmBinaryOnStdout(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	src := writeShippedSrc(t, t.TempDir(), "main.fern", shippedArgsSrc)
	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(cli, append(args, src, root)...)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("fern %s did not exit normally\n%s", strings.Join(args, " "), stderr.String())
		}
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-target", "wasm32-wasi"}, "fern: -target wasm32-wasi requires -o OUTPUT (the component is a binary)\n"},
		{[]string{"-target", "wasm32-wasi-http"}, "fern: -target wasm32-wasi-http requires -o OUTPUT (the component is a binary)\n"},
		{[]string{"-target", "wasm32-wasi", "-emit", "core-module"}, "fern: -emit core-module for -target wasm32-wasi writes a binary; pass -o OUTPUT\n"},
		{[]string{"-target", "wasm32-wasi-http", "-emit", "core-module"}, "fern: -emit core-module for -target wasm32-wasi-http writes a binary; pass -o OUTPUT\n"},
		{[]string{"-target", "wasm32-wasi", "-emit", "command-module"}, "fern: -emit command-module for -target wasm32-wasi writes a binary; pass -o OUTPUT\n"},
	} {
		stdout, stderr, code := run(c.args...)
		if code != 2 || stderr != c.want || stdout != "" {
			t.Errorf("fern %s: exit %d, stderr %q, %d bytes on stdout; want exit 2, stderr %q, nothing on stdout",
				strings.Join(c.args, " "), code, stderr, len(stdout), c.want)
		}
	}
	if stdout, stderr, code := run("-target", "wasm32-wasi", "-emit", "asm"); code != 0 || !strings.HasPrefix(stdout, "(module") {
		t.Errorf("fern -target wasm32-wasi -emit asm: exit %d, stdout %.80q; want exit 0 and WAT\n%s", code, stdout, stderr)
	}
	if _, stderr, code := run("-check", "-target", "wasm32-wasi"); code != 0 {
		t.Errorf("fern -check -target wasm32-wasi: exit %d, want 0\n%s", code, stderr)
	}
}
