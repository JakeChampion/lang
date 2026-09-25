package e2eselfhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FERN_SEM_IR_STRICT=1 turns the silent fallback to the AST lowering into a
// failed compile: exit 3, after the refusals FERN_SEM_IR_REPORT would print.
// A module the typed path produces whole compiles as it would without the flag,
// and without the flag a refused one still compiles. A runtime helper the typed
// path refuses fails the compile the same way.
func TestSelfHostSemIRStrict(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	compileFor := func(target, src string, env ...string) (int, string) {
		path := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(fernBin, "-target", target, path, stdlibRoot, "-o", filepath.Join(t.TempDir(), "prog"))
		cmd.Env = append(os.Environ(), env...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), stderr.String()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0, stderr.String()
	}
	compile := func(src string, env ...string) (int, string) {
		return compileFor("x86-64-linux", src, env...)
	}

	produced := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(2, 3); }\n"
	if code, out := compile(produced, "FERN_SEM_IR_STRICT=1"); code != 0 {
		t.Fatalf("a module produced whole: exit %d under strict\n%s", code, out)
	}

	// Each target spells the clock, id and termios helpers' sources on its own
	// syscalls. termios_get calls the fs bundle's __fern_io_error.
	helpers := `function main(): i32 {
    var ids: u32 = geteuid() + getegid() + getuid() + getgid();
    var t: i64 = monotonic_ns() + now_unix_ms() + now_ns();
    if (t < 0 || ids == 4294967295 as u32) { return 1; }
    match (termios_get(99)) {
        Ok(words) => { return words.len(); },
        Err(e) => { return 0; }
    }
}
`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if code, out := compileFor(target, helpers, "FERN_SEM_IR_STRICT=1"); code != 0 {
			t.Errorf("%s: the runtime helpers: exit %d under strict\n%s", target, code, out)
		}
	}

	// An array holding views of two parameters has no one argument to anchor to.
	refused := `function g(x: string, y: string): str[] { var o: str[] = []; o = o.append(slice_unchecked(x, 0, 1)); o = o.append(slice_unchecked(y, 0, 1)); return o; }
function main(): i32 { var xs: str[] = g("ab", "cd"); return xs.len(); }
`
	code, out := compile(refused, "FERN_SEM_IR_STRICT=1")
	if code != 3 {
		t.Fatalf("a refused module: exit %d under strict, want 3\n%s", code, out)
	}
	if !strings.Contains(out, "FERN_SEM_IR: g:") || !strings.Contains(out, "FERN_SEM_IR_STRICT") {
		t.Fatalf("strict did not name the refusal:\n%s", out)
	}
	if code, out := compile(refused, "FERN_SEM_IR_STRICT="); code != 0 {
		t.Fatalf("a refused module without strict: exit %d, want the AST lowering's compile\n%s", code, out)
	}
}
