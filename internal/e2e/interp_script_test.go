package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildLangBinForInterp compiles cmd/lang to a temp binary the
// `-interp` script-mode tests below invoke. Each test gets its
// own t.TempDir() but the build is cheap enough (Go's incremental
// build cache makes the second+ invocation a few-ms hit).
func buildLangBinForInterp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "lang")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/lang")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lang: %v\n%s", err, out)
	}
	return bin
}

// `lang -interp FILE.lang` — script-mode end-to-end. The
// interpreter runs the lang program through the AST evaluator
// (no codegen, no link, no temp binary) and main()'s return
// value becomes the process exit code, clamped to 0..255.
// Mirrors `python script.py` semantics.
func TestInterpScriptFile(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.lang")
	if err := os.WriteFile(src, []byte(`function fact(n: i32): i32 {
    if (n == 0) { return 1; }
    return n * fact(n - 1);
}
function main(): i32 {
    var f: i32 = fact(5);
    print(f.to_string());
    return f;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 120 {
		t.Errorf("exit = %d, want 120 (5!)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "120") {
		t.Errorf("stdout missing `120`: %q", out.String())
	}
}

// `lang -interp -` — pipe a program through stdin. Skips
// modload entirely (no imports available in the stdin form);
// parses the buffer as a single file.
func TestInterpScriptStdin(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`function main(): i32 {
    print("from stdin");
    return 42;
}`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "from stdin") {
		t.Errorf("stdout missing payload: %q", out.String())
	}
}

// read_all_stdin() — convenience prelude function that loops
// Reader.read_chunk(4096) until EOF and returns the concat.
// Writes the program to a file so `-interp <file>` reads the
// SOURCE from disk and leaves stdin free for the program to
// consume via the helper.
func TestInterpScriptReadAllStdin(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.lang")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
    var s: string = read_all_stdin();
    print("read: " + s);
    return len(s);
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	cmd.Stdin = strings.NewReader("hello stdin")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 11 {
		t.Errorf("exit = %d, want 11 (len of \"hello stdin\")\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "read: hello stdin") {
		t.Errorf("stdout missing payload: %q", out.String())
	}
}

// Union types + match through the script-mode interp. The union
// desugar lives in the checker (PRs #390 / #392), so the AST
// the interpreter sees is the synthesised enum form — no
// special-case path needed in the interpreter for unions.
func TestInterpScriptUnions(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`struct Add { l: i32, r: i32 }
struct Lit { v: i32 }
type Expr = Add | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Lit(l) => { return l.v; },
    }
}

function main(): i32 {
    var e: Expr = Add { l: 10, r: 32 };
    return eval(e);
}`)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42 (union via interp)", code)
	}
}

// Prelude string helpers that call into raw-memory primitives
// (`s.bytes()` / `s.as_bytes()` lower to `__memcpy(out as i32,
// s.as_bytes() as i32, n)`; `s.to_lower()` / `s.to_upper()`
// route through `__string_case_fold` which uses `__alloc_u8`
// + `string_from_bytes`) all need to round-trip the
// String→Array conversion without leaning on a flat byte
// address space. This test exercises every helper so a future
// prelude rewrite that drops a builtin override would surface
// here rather than as a silent regression in the playground.
func TestInterpScriptStringPrelude(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`function main(): i32 {
    var s: string = "Hello";
    var bs: u8[] = s.bytes();
    if (len(bs) != 5) { return 1; }
    if (bs[0] != 72) { return 2; }
    if (bs[4] != 111) { return 3; }
    var ab: [u8] = s.as_bytes();
    if (len(ab) != 5) { return 4; }
    if (ab[0] != 72) { return 5; }
    if (s.to_upper() != "HELLO") { return 6; }
    if (s.to_lower() != "hello") { return 7; }
    var rt: string = string_from_bytes(s.bytes());
    if (rt != "Hello") { return 8; }
    print(rt);
    print(s.to_upper());
    print(s.to_lower());
    return 0;
}`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	want := "Hello\nHELLO\nhello\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

// A program that explicitly imports a stdlib module whose
// transitive imports include `core/int` (or imports `core/int`
// directly) hits the modload mangling path: the function name
// becomes `int__int_to_string` instead of bare `int_to_string`.
// The interp's Go override for that function is keyed under
// the bare name, so without an alias the mangled call falls
// through to the Lang body and crashes on the unrepresentable
// `scratch as i32` cast.
//
// We exercise four shapes of the same bug so a regression
// shows up wherever it surfaces:
//
//   1. Explicit `import "core/int";` + bare `(n).to_string()`
//      — the smallest reproducer.
//   2. Explicit `import "std/json";` — drags in core/int
//      transitively without the user reaching for it directly.
//      This is what tripped over the std/test PR review.
//   3. Direct `int.int_to_string(n)` qualified call against
//      the explicit `core/int` import — same mangling, but
//      the call site is the user's own qualified reference
//      rather than the receiver-method dispatch hoist.
//   4. The auto-prelude path (no extra imports) — sanity
//      check that the alias doesn't break the original
//      flat-load route.
//
// Each shape writes a `.lang` file to a tempdir and runs
// `lang -interp FILE` rather than piping over stdin: the
// stdin path skips modload entirely (no imports), so it
// wouldn't exercise the mangling code path the fix targets.
func TestInterpScriptInteropIntToStringViaMangling(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source string
	}{
		{
			name: "explicit core/int + method dispatch",
			source: `import "core/int";

function main(): i32 {
    var x: i32 = 5;
    print(x.to_string());
    return 0;
}`,
		},
		{
			name: "transitive via std/json",
			source: `import "std/json";

function main(): i32 {
    var x: i32 = 7;
    print(x.to_string());
    return 0;
}`,
		},
		{
			name: "qualified int.int_to_string call",
			source: `import "core/int";

function main(): i32 {
    var s: string = int.int_to_string(11);
    print(s);
    return 0;
}`,
		},
		{
			name: "auto-prelude flat-load path (regression sanity)",
			source: `function main(): i32 {
    var x: i32 = 42;
    print(x.to_string());
    return 0;
}`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.lang")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			cmd := exec.Command(bin, "-interp", src)
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
					code, out.String(), errb.String())
			}
			if strings.Contains(errb.String(), "interp.Array to i32") {
				t.Fatalf("interp leaked the Array→i32 cast error:\nstderr: %s",
					errb.String())
			}
			if out.Len() == 0 {
				t.Errorf("expected `to_string` output on stdout, got empty\nstderr: %s",
					errb.String())
			}
		})
	}
}

// TestInterpScriptHeaderMap pins the HeaderMap surface from
// `std/headers` — case-insensitive `.get` / `.get_all`,
// duplicate-preserving `.append`, position-stable `.set` (drops
// any extra duplicates of the same name), and `.size()` matching
// total entries. Anchors docs/STDLIB-DESIGN-RESEARCH.md Rec §2
// before the HttpRequest / HttpResponse integration lands.
func TestInterpScriptHeaderMap(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
		wantExit                 int
	}{
		{
			name: "case-insensitive get",
			source: `import "std/headers";
function main(): i32 {
    var h: HeaderMap = headers.header_map_new();
    h.set("Content-Type", "application/json");
    match (h.get("CONTENT-TYPE")) {
        Some(v) => { print(v); },
        None => { print("MISSING"); }
    }
    return 0;
}`,
			wantStdout: "application/json\n",
		},
		{
			name: "get_all preserves duplicates in insertion order",
			source: `import "std/headers";
function main(): i32 {
    var h: HeaderMap = headers.header_map_new();
    h.append("Set-Cookie", "a=1");
    h.append("Set-Cookie", "b=2");
    h.append("Set-Cookie", "c=3");
    var all: string[] = h.get_all("set-cookie");
    var i: i32 = 0;
    while (i < len(all)) {
        print(all[i]);
        i = i + 1;
    }
    return 0;
}`,
			wantStdout: "a=1\nb=2\nc=3\n",
		},
		{
			name: "set replaces in place and drops other duplicates",
			source: `import "std/headers";
function main(): i32 {
    var h: HeaderMap = headers.header_map_new();
    h.append("X", "first");
    h.append("Y", "y1");
    h.append("X", "second");
    h.set("x", "replaced");
    match (h.get("x")) {
        Some(v) => { print(v); },
        None => { print("MISSING"); }
    }
    return h.size();
}`,
			wantStdout: "replaced\n",
			wantExit:   2,
		},
		{
			name: "set on absent name appends",
			source: `import "std/headers";
function main(): i32 {
    var h: HeaderMap = headers.header_map_new();
    h.set("X-First", "1");
    return h.size();
}`,
			wantExit: 1,
		},
		{
			name: "get_all on missing name is empty",
			source: `import "std/headers";
function main(): i32 {
    var h: HeaderMap = headers.header_map_new();
    h.append("X", "1");
    return len(h.get_all("Y"));
}`,
			wantExit: 0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.lang")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			cmd := exec.Command(bin, "-interp", src)
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s",
					code, tc.wantExit, out.String(), errb.String())
			}
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q, want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// A program without `main` exits non-zero with a clear error.
// Catches the case where someone pipes a snippet (function
// helpers only) and expects the interpreter to find an entry
// point.
func TestInterpScriptMissingMain(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`function helper(): i32 { return 1; }`)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("exit = 0, expected non-zero on missing main")
	}
	if !strings.Contains(errb.String(), "no `main`") && !strings.Contains(errb.String(), "no main") {
		t.Errorf("stderr did not mention missing main: %q", errb.String())
	}
}
