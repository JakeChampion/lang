package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -interp FILE.fern` — script-mode end-to-end. The
// interpreter runs the lang program through the AST evaluator
// (no codegen, no link, no temp binary) and main()'s return
// value becomes the process exit code, clamped to 0..255.
// Mirrors `python script.py` semantics.
func TestInterpScriptFile(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`
import "std/i32";
function fact(n: i32): i32 {
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

// `fern -interp -` — pipe a program through stdin. Skips
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
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`
import "std/io";
function main(): i32 {
    var s: string = io.read_all_stdin();
    print("read: " + s);
    return s.len();
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

// Bare `read_line()` builtin through the interpreter. Reads one
// line from stdin and returns its length via the Some arm (0 at
// EOF via None). Mirrors the wasm / native read_line coverage.
func TestInterpScriptReadLine(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
    match (read_line()) {
        Some(line) => { return line.len(); },
        None => { return 0; }
    }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	run := func(stdin string, want int) {
		t.Helper()
		cmd := exec.Command(bin, "-interp", src)
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		_ = cmd.Run()
		if got := cmd.ProcessState.ExitCode(); got != want {
			t.Errorf("stdin=%q: exit = %d, want %d\nstderr: %s", stdin, got, want, errb.String())
		}
	}
	run("hi\n", 3) // "hi\n" → len 3
	run("", 0)     // EOF → None → 0
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
// + `string_from_bytes_unchecked`) all need to round-trip the
// String→Array conversion without leaning on a flat byte
// address space. This test exercises every helper so a future
// prelude rewrite that drops a builtin override would surface
// here rather than as a silent regression in the playground.
func TestInterpScriptStringPrelude(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/string";
function main(): i32 {
    var s: string = "Hello";
    var bs: u8[] = s.bytes();
    if (bs.len() != 5) { return 1; }
    if (bs[0] != 72) { return 2; }
    if (bs[4] != 111) { return 3; }
    var ab: [u8] = s.as_bytes();
    if (ab.len() != 5) { return 4; }
    if (ab[0] != 72) { return 5; }
    if (s.to_upper() != "HELLO") { return 6; }
    if (s.to_lower() != "hello") { return 7; }
    var rt: string = string_from_bytes_unchecked(s.bytes());
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

// Random / byte builtins on the interpreter (issue #2747). The
// interp historically lacked `random_i32` entirely (`undefined
// function "random_i32"`); this locks in the fix and asserts
// cross-backend agreement on the shapes the native + wasm
// backends already support:
//
//   - random_i32() is callable and varies across draws.
//   - random_bytes(n).len() == n even when the random payload
//     contains embedded NUL bytes (the interp String is length-
//     prefixed, not NUL-terminated).
//   - s.as_bytes().len() / indexing match the source string.
func TestInterpScriptRandomAndBytes(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
function main(): i32 {
    // random_i32 is live and varying.
    var a: i32 = random_i32();
    var b: i32 = random_i32();
    if (a == b) { return 1; }
    // random_bytes length is exact regardless of NUL bytes.
    if (random_bytes(8).len() != 8) { return 2; }
    if (random_bytes(0).len() != 0) { return 3; }
    // as_bytes view length + byte values match the source.
    var bs: [u8] = "ABC".as_bytes();
    if (bs.len() != 3) { return 4; }
    if ((bs[0] as i32) != 65) { return 5; }
    if ((bs[2] as i32) != 67) { return 6; }
    return 0;
}`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// std/crypto (#2681): SHA-256 + HMAC-SHA256 against standard
// known-answer vectors on the interpreter. (x86-64 + wasm have their
// own crypto tests; arm64 is skipped pending the #2768 freelist bug.)
func TestInterpScriptCrypto(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(cryptoVectorsProgram)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (failed crypto vector)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// std/uuid (#2682): uuid_v4 / uuid_v7 generation, built on the
// cross-backend random_bytes byte source (#2747). Asserts the
// canonical 36-char 8-4-4-4-12 shape, the version + variant
// nibbles, that it validates via string.is_uuid(), and that two
// draws differ.
func TestInterpScriptUuid(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/uuid";
import "std/string";
function main(): i32 {
    var a: string = uuid.uuid_v4();
    if (a.len() != 36) { return 1; }
    if (!a.is_uuid()) { return 2; }
    if (a[14] != 52) { return 3; }          // version '4'
    if (a[8] != 45 || a[13] != 45 || a[18] != 45 || a[23] != 45) { return 4; }
    var b: string = uuid.uuid_v7();
    if (b.len() != 36) { return 5; }
    if (!b.is_uuid()) { return 6; }
    if (b[14] != 55) { return 7; }          // version '7'
    if (uuid.uuid_v4() == uuid.uuid_v4()) { return 8; }
    return 0;
}`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
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
//  1. Explicit `import "core/int";` + bare `(n).to_string()`
//     — the smallest reproducer.
//  2. Explicit `import "std/json";` — drags in core/int
//     transitively without the user reaching for it directly.
//     This is what tripped over the std/test PR review.
//  3. Direct `int.int_to_string(n)` qualified call against
//     the explicit `core/int` import — same mangling, but
//     the call site is the user's own qualified reference
//     rather than the receiver-method dispatch hoist.
//  4. The auto-prelude path (no extra imports) — sanity
//     check that the alias doesn't break the original
//     flat-load route.
//
// Each shape writes a `.fern` file to a tempdir and runs
// `fern -interp FILE` rather than piping over stdin: the
// stdin path skips modload entirely (no imports), so it
// wouldn't exercise the mangling code path the fix targets.
func TestInterpScriptInteropIntToStringViaMangling(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source string
	}{
		{
			name: "explicit core/int + method dispatch",
			source: `
import "std/i32";
import "core/int";

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
			source: `
import "std/i32";
function main(): i32 {
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
			src := filepath.Join(dir, "prog.fern")
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

// TestInterpScriptLambdaBodyMangling pins the #4802 mangler fix
// end-to-end: a module whose functions reference module-local
// declarations from exactly the shapes the rewriter used to miss —
// a bare fn reference in arg position (`apply(add_one, x)`) and
// module-local calls/refs from inside lambda bodies — plus the
// shadow rules (a lambda param / body local / captured enclosing
// local named like a module decl must stay bound to the local).
// Before the fix, importing such a module failed E001 on the
// lambda-body references. Runs through -interp with real files
// (the file path exercises modload; stdin would skip it). The
// modload-layer sibling is internal/modload's
// TestLambdaBodyManglingChecks; the self-host loader sibling is
// TestSelfHostLambdaMangleX86_64.
func TestInterpScriptLambdaBodyMangling(t *testing.T) {
	bin := buildLangBinForInterp(t)
	lib := `pub function add_one(x: i32): i32 { return x + 1; }

pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }

pub function via_bare(x: i32): i32 { return apply(add_one, x); }

pub function via_lambda(x: i32): i32 {
    return apply(function (v: i32): i32 { return add_one(v) + 10; }, x);
}

pub function via_lambda_ref(x: i32): i32 {
    return apply(function (v: i32): i32 { return apply(add_one, v) + 100; }, x);
}

pub function shadow_param(x: i32): i32 {
    return apply(function (add_one: i32): i32 { return add_one * 2; }, x);
}

pub function shadow_local(x: i32): i32 {
    return apply(function (v: i32): i32 { var add_one: i32 = 7; return v + add_one; }, x);
}

pub function shadow_capture(x: i32): i32 {
    var add_one: i32 = 50;
    return apply(function (v: i32): i32 { return v + add_one; }, x);
}
`
	main := `import "./lamlib";
function main(): i32 {
    if (lamlib.via_bare(1) != 2) { return 1; }
    if (lamlib.via_lambda(1) != 12) { return 2; }
    if (lamlib.via_lambda_ref(1) != 102) { return 3; }
    if (lamlib.shadow_param(3) != 6) { return 4; }
    if (lamlib.shadow_local(3) != 10) { return 5; }
    if (lamlib.shadow_capture(3) != 53) { return 6; }
    print("OK");
    return 0;
}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lamlib.fern"), []byte(lib), 0o644); err != nil {
		t.Fatalf("write lamlib: %v", err)
	}
	src := filepath.Join(dir, "lambda_mangle.fern")
	if err := os.WriteFile(src, []byte(main), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 (1=via_bare 2=via_lambda 3=via_lambda_ref 4=shadow_param 5=shadow_local 6=shadow_capture)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("expected OK on stdout, got %q\nstderr: %s", out.String(), errb.String())
	}
}

// TestInterpScriptHeaderMap pins the HeaderMap surface from
// `std/headers` — case-insensitive `.get` / `.get_all`,
// duplicate-preserving `.append`, position-stable `.set` (drops
// any extra duplicates of the same name), and `.len()` matching
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
    h = h.set("Content-Type", "application/json");
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
    h = h.append("Set-Cookie", "a=1");
    h = h.append("Set-Cookie", "b=2");
    h = h.append("Set-Cookie", "c=3");
    var all: string[] = h.get_all("set-cookie");
    var i: i32 = 0;
    while (i < all.len()) {
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
    h = h.append("X", "first");
    h = h.append("Y", "y1");
    h = h.append("X", "second");
    h = h.set("x", "replaced");
    match (h.get("x")) {
        Some(v) => { print(v); },
        None => { print("MISSING"); }
    }
    return h.len();
}`,
			wantStdout: "replaced\n",
			wantExit:   2,
		},
		{
			name: "set on absent name appends",
			source: `import "std/headers";
function main(): i32 {
    var h: HeaderMap = headers.header_map_new();
    h = h.set("X-First", "1");
    return h.len();
}`,
			wantExit: 1,
		},
		{
			name: "get_all on missing name is empty",
			source: `import "std/headers";
function main(): i32 {
    var h: HeaderMap = headers.header_map_new();
    h = h.append("X", "1");
    return (h.get_all("Y")).len();
}`,
			wantExit: 0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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

// TestInterpScriptHttpRequestHeaders pins the HeaderMap →
// HttpRequest wiring (docs/STDLIB-DESIGN-RESEARCH.md Rec §2).
// `http_parse_request` populates `req.headers` from the wire
// header block; the handler reads values back via the case-
// insensitive `HeaderMap.get` surface introduced in #858.
func TestInterpScriptHttpRequestHeaders(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
		wantExit                 int
	}{
		{
			name: "parsed headers reachable on req.headers",
			source: `import "std/http";

function main(): i32 {
    var wire: string = "GET /x HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\nContent-Length: 0\r\n\r\n";
    match (http.http_parse_request(wire)) {
        Some(req) => {
            match (req.headers.get("content-type")) {
                Some(v) => { print(v); },
                None => { print("MISSING"); }
            }
            return req.headers.len();
        },
        None => {
            print("PARSE_FAIL");
            return 99;
        }
    }
    return 0;
}`,
			wantStdout: "application/json\n",
			wantExit:   3,
		},
		{
			name: "duplicate header preserved in insertion order",
			source: `import "std/http";

function main(): i32 {
    var wire: string = "GET / HTTP/1.1\r\nSet-Cookie: a=1\r\nSet-Cookie: b=2\r\nContent-Length: 0\r\n\r\n";
    match (http.http_parse_request(wire)) {
        Some(req) => {
            var all: string[] = req.headers.get_all("Set-Cookie");
            var i: i32 = 0;
            while (i < all.len()) {
                print(all[i]);
                i = i + 1;
            }
            return 0;
        },
        None => { return 99; }
    }
    return 0;
}`,
			wantStdout: "a=1\nb=2\n",
		},
		{
			name: "missing header returns None",
			source: `import "std/http";

function main(): i32 {
    var wire: string = "GET / HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n";
    match (http.http_parse_request(wire)) {
        Some(req) => {
            match (req.headers.get("X-Absent")) {
                Some(v) => { print(v); return 1; },
                None => { print("none"); return 0; }
            }
        },
        None => { return 99; }
    }
    return 0;
}`,
			wantStdout: "none\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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

// TestInterpScriptHttpResponseHeaders pins the HeaderMap →
// HttpResponse wiring + http_serialize_response emission
// behaviour (docs/STDLIB-DESIGN-RESEARCH.md Rec §2). Handlers
// can `resp.headers.set/append` and those headers land in the
// wire output ahead of the auto-emitted Content-Length /
// Connection block. The auto block always wins for those two
// names so a misconfigured handler can't ship a malformed
// response.
func TestInterpScriptHttpResponseHeaders(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
		wantExit                 int
	}{
		{
			name: "user-set headers appear before the auto block",
			source: `import "std/http";

function main(): i32 {
    var r: HttpResponse = http.http_response_ok("hello");
    r = r.with_header("X-Trace-Id", "abc123");
    r = r.with_header("Cache-Control", "no-store");
    var wire: string = http.http_serialize_response(r);
    print(wire);
    return 0;
}`,
			wantStdout: "HTTP/1.1 200 OK\r\nx-trace-id: abc123\r\ncache-control: no-store\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello\n",
		},
		{
			name: "redirect helper sets Location header",
			source: `import "std/http";

function main(): i32 {
    var r: HttpResponse = http.http_response_redirect("/login");
    match (r.headers.get("location")) {
        Some(v) => { print(v); },
        None => { print("MISSING"); }
    }
    return 0;
}`,
			wantStdout: "/login\n",
		},
		{
			name: "user-set Content-Length is ignored in favor of auto",
			source: `import "std/http";

function main(): i32 {
    var r: HttpResponse = http.http_response_ok("hi");
    r = r.with_header("Content-Length", "9999");
    var wire: string = http.http_serialize_response(r);
    print(wire);
    return 0;
}`,
			wantStdout: "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi\n",
		},
		{
			name: "duplicate Set-Cookie via append both appear",
			source: `import "std/http";

function main(): i32 {
    var r: HttpResponse = http.http_response_ok("hi");
    r = r.with_appended_header("Set-Cookie", "a=1");
    r = r.with_appended_header("Set-Cookie", "b=2");
    var wire: string = http.http_serialize_response(r);
    print(wire);
    return 0;
}`,
			wantStdout: "HTTP/1.1 200 OK\r\nset-cookie: a=1\r\nset-cookie: b=2\r\nContent-Length: 2\r\nConnection: close\r\n\r\nhi\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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

// TestInterpScriptTimeTypes pins the type registrations + the
// Phase-1 constructors from std/time
// (docs/STDLIB-DESIGN-RESEARCH.md Rec §4). Each subtest exercises
// one of the seven date/time types as a struct literal + a
// stdlib constructor, then reads a field back. Anchors the
// shape against accidental regressions while the rest of the
// module (now(), arithmetic, RFC 3339, IANA zones) lands in
// follow-up PRs.
func TestInterpScriptTimeTypes(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
		wantExit                 int
	}{
		{
			name: "Instant from unix seconds",
			source: `
import "std/i64";
import "std/time";
function main(): i32 {
    var ts: Instant = time.instant_from_unix(1700000000 as i64);
    print(ts.sec.to_string());
    return ts.nsec;
}`,
			wantStdout: "1700000000\n",
		},
		{
			name: "Date struct fields round-trip",
			source: `import "std/time";
function main(): i32 {
    var d: Date = time.date_make(2026, 5, 19);
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
    return d.day;
}`,
			wantStdout: "2026-5-19\n",
			wantExit:   19,
		},
		{
			name: "Time at second precision (nsec zero)",
			source: `import "std/time";
function main(): i32 {
    var t: Time = time.time_make(14, 30, 45);
    print(t.hour.to_string() + ":" + t.minute.to_string() + ":" + t.second.to_string());
    return t.nsec;
}`,
			wantStdout: "14:30:45\n",
		},
		{
			name: "DateTime composes Date + Time",
			source: `import "std/time";
function main(): i32 {
    var dt: DateTime = time.datetime_make(time.date_make(2026, 1, 1), time.time_make(0, 0, 0));
    print((dt.date.year - dt.time.hour).to_string());
    return 0;
}`,
			wantStdout: "2026\n",
		},
		{
			name: "Duration milliseconds splits sec + nsec",
			source: `
import "std/i64";
import "std/time";
function main(): i32 {
    var d: Duration = time.duration_millis(2500 as i64);
    print(d.sec.to_string());
    print(d.nsec.to_string());
    return 0;
}`,
			wantStdout: "2\n500000000\n",
		},
		{
			name: "Span days vs hours are distinct fields",
			source: `import "std/time";
function main(): i32 {
    var dz: Span = time.span_days(7);
    var hr: Span = time.span_hours(24);
    print((dz.days * 100 + hr.hours).to_string());
    return 0;
}`,
			wantStdout: "724\n",
		},
		{
			name: "TimeZone UTC carries name + zero offset",
			source: `import "std/time";
function main(): i32 {
    var utc: TimeZone = time.timezone_utc();
    print(utc.name);
    return utc.offset_seconds;
}`,
			wantStdout: "UTC\n",
		},
		{
			name: "Zoned struct composes Instant + TimeZone",
			source: `import "std/time";
function main(): i32 {
    var z: Zoned = Zoned {
        instant: time.instant_from_unix(0 as i64),
        zone: time.timezone_utc(),
    };
    print(z.zone.name);
    return z.instant.nsec;
}`,
			wantStdout: "UTC\n",
		},
		{
			name: "Constants exposed for callers",
			source: `import "std/time";
function main(): i32 {
    print((time.SECONDS_PER_HOUR + time.MINUTES_PER_HOUR + time.HOURS_PER_DAY + time.DAYS_PER_WEEK).to_string());
    return 0;
}`,
			wantStdout: "3691\n", // 3600 + 60 + 24 + 7
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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

// TestInterpScriptInstantNow pins `std/time.instant_now()`
// (docs/STDLIB-DESIGN-RESEARCH.md Rec §4 Phase 2). Reads the
// current wall-clock time and asserts the seconds field is
// in a plausible range — past a sentinel epoch second and
// before a far-future bound. Catches accidental sign / unit
// mistakes without baking in a brittle exact value.
func TestInterpScriptInstantNow(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := `
import "std/string";
import "std/time";

function main(): i32 {
    var ts: Instant = time.instant_now();
    // 1700000000 = 2023-11-14T22:13:20Z. Anything before is
    // either a clock catastrophe or a sign-handling bug.
    if (ts.sec < (1700000000 as i64)) { return 1; }
    // Year 9999-ish upper bound; if we somehow returned
    // micro/nanos instead of milliseconds-converted-to-
    // seconds, we'd land past this.
    if (ts.sec > (253402300800 as i64)) { return 2; }
    // nsec carries millisecond precision today (the underlying
    // primitive is now_unix_ms), so 0 <= nsec < 1e9 and
    // divisible by 1e6.
    if (ts.nsec < 0) { return 3; }
    if (ts.nsec >= 1000000000) { return 4; }
    return 0;
}`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
}

// TestInterpScriptCivilDateArith pins the Phase-3 Hinnant
// civil-date helpers (docs/STDLIB-DESIGN-RESEARCH.md Rec §4):
// add_days, weekday, day_of_year, days_since, is_valid,
// is_leap_year, days_in_month. Pure-function arithmetic
// — no system clock involved, so the tests pin exact values
// across month / year / leap-year / pre-epoch boundaries.
func TestInterpScriptCivilDateArith(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "add_days crosses month + year boundaries",
			source: `import "std/time";
function show(d: Date) {
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
}
function main(): i32 {
    show(time.date_make(2026, 1, 31).add_days(1));
    show(time.date_make(2026, 12, 31).add_days(1));
    show(time.date_make(2026, 1, 1).add_days(-1));
    return 0;
}`,
			wantStdout: "2026-2-1\n2027-1-1\n2025-12-31\n",
		},
		{
			name: "add_days respects leap years",
			source: `import "std/time";
function show(d: Date) {
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
}
function main(): i32 {
    show(time.date_make(2024, 2, 28).add_days(1));
    show(time.date_make(2024, 2, 29).add_days(1));
    show(time.date_make(2025, 2, 28).add_days(1));
    return 0;
}`,
			wantStdout: "2024-2-29\n2024-3-1\n2025-3-1\n",
		},
		{
			name: "weekday matches known calendar dates (Sun=0)",
			source: `import "std/time";
function main(): i32 {
    // 1970-01-01 was a Thursday.
    print(time.date_make(1970, 1, 1).weekday().to_string());
    // 2024-01-01 was a Monday.
    print(time.date_make(2024, 1, 1).weekday().to_string());
    // 2025-12-25 was a Thursday.
    print(time.date_make(2025, 12, 25).weekday().to_string());
    // 2026-05-19 (today's date per CLAUDE.md) was a Tuesday.
    print(time.date_make(2026, 5, 19).weekday().to_string());
    return 0;
}`,
			wantStdout: "4\n1\n4\n2\n",
		},
		{
			name: "day_of_year handles Jan / Feb / Mar / Dec in leap + non-leap",
			source: `import "std/time";
function main(): i32 {
    print(time.date_make(2026, 1, 1).day_of_year().to_string());    // 1
    print(time.date_make(2026, 2, 28).day_of_year().to_string());   // 59
    print(time.date_make(2026, 3, 1).day_of_year().to_string());    // 60
    print(time.date_make(2026, 12, 31).day_of_year().to_string());  // 365
    print(time.date_make(2024, 2, 29).day_of_year().to_string());   // 60
    print(time.date_make(2024, 3, 1).day_of_year().to_string());    // 61
    print(time.date_make(2024, 12, 31).day_of_year().to_string());  // 366
    return 0;
}`,
			wantStdout: "1\n59\n60\n365\n60\n61\n366\n",
		},
		{
			name: "days_since for various intervals",
			source: `import "std/time";
function main(): i32 {
    // Same day.
    print(time.date_make(2026, 5, 19).days_since(time.date_make(2026, 5, 19)).to_string());
    // 2026 is non-leap, so one year = 365 days.
    print(time.date_make(2027, 1, 1).days_since(time.date_make(2026, 1, 1)).to_string());
    // 2024 is leap, so one year = 366 days.
    print(time.date_make(2025, 1, 1).days_since(time.date_make(2024, 1, 1)).to_string());
    // Negative (other is after).
    print(time.date_make(2026, 1, 1).days_since(time.date_make(2026, 1, 8)).to_string());
    return 0;
}`,
			wantStdout: "0\n365\n366\n-7\n",
		},
		{
			name: "is_valid rejects bad month/day/feb-30",
			source: `import "std/time";
function show_v(d: Date) {
    if (d.is_valid()) { print("valid"); } else { print("invalid"); }
}
function main(): i32 {
    show_v(time.date_make(2026, 1, 1));
    show_v(time.date_make(2026, 13, 1));     // bad month
    show_v(time.date_make(2026, 2, 30));     // Feb 30
    show_v(time.date_make(2024, 2, 29));     // Feb 29 in leap
    show_v(time.date_make(2025, 2, 29));     // Feb 29 in non-leap
    show_v(time.date_make(2026, 0, 1));      // month 0
    show_v(time.date_make(2026, 5, 0));      // day 0
    show_v(time.date_make(2026, 4, 31));     // Apr has 30 days
    return 0;
}`,
			wantStdout: "valid\ninvalid\ninvalid\nvalid\ninvalid\ninvalid\ninvalid\ninvalid\n",
		},
		{
			name: "leap year rule (4 / 100 / 400)",
			source: `import "std/time";
function show(y: i32) {
    if (time.is_leap_year(y)) { print(y.to_string() + " leap"); } else { print(y.to_string() + " no"); }
}
function main(): i32 {
    show(2024);   // div 4, not 100  -> leap
    show(2025);   // not div 4       -> no
    show(2000);   // div 400         -> leap
    show(1900);   // div 100, not 400 -> no
    show(2100);   // div 100, not 400 -> no
    show(1600);   // div 400         -> leap
    return 0;
}`,
			wantStdout: "2024 leap\n2025 no\n2000 leap\n1900 no\n2100 no\n1600 leap\n",
		},
		{
			name: "pre-epoch dates round-trip through add_days",
			source: `import "std/time";
function show(d: Date) {
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
}
function main(): i32 {
    // Walk back 1 day from 1970-01-01 → 1969-12-31.
    show(time.date_make(1970, 1, 1).add_days(-1));
    // Walk back exactly one (non-leap) year from 1970-01-01.
    show(time.date_make(1970, 1, 1).add_days(-365));
    return 0;
}`,
			wantStdout: "1969-12-31\n1969-1-1\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q, want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptRfc3339 pins the Phase-4 parse + format
// surface (docs/STDLIB-DESIGN-RESEARCH.md Rec §4): ISO date
// (`YYYY-MM-DD`) for `Date`, RFC 3339 UTC instant
// (`YYYY-MM-DDTHH:MM:SS[.fraction]Z`) for `Instant`.
// Zoned offsets `+HH:MM` land with Phase 5.
func TestInterpScriptRfc3339(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "Date.format_iso zero-pads month + day",
			source: `import "std/time";
function main(): i32 {
    print(time.date_make(2026, 5, 19).format_iso());
    print(time.date_make(2024, 1, 1).format_iso());
    print(time.date_make(2024, 12, 31).format_iso());
    print(time.date_make(99, 3, 7).format_iso());
    return 0;
}`,
			wantStdout: "2026-05-19\n2024-01-01\n2024-12-31\n0099-03-07\n",
		},
		{
			name: "date_parse_iso accepts canonical form",
			source: `import "std/time";
function show(opt: Option[Date]) {
    match (opt) {
        Some(d) => { print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string()); },
        None => { print("none"); }
    }
}
function main(): i32 {
    show(time.date_parse_iso("2026-05-19"));
    show(time.date_parse_iso("2024-02-29"));   // leap-year day
    show(time.date_parse_iso("0099-03-07"));   // zero-padded year
    return 0;
}`,
			wantStdout: "2026-5-19\n2024-2-29\n99-3-7\n",
		},
		{
			name: "date_parse_iso rejects malformed input",
			source: `import "std/time";
function show(s: string) {
    match (time.date_parse_iso(s)) {
        Some(_) => { print("ACCEPTED " + s); },
        None => { print("rejected " + s); }
    }
}
function main(): i32 {
    show("");
    show("2026/05/19");        // wrong separator
    show("2026-05-19T00");     // too long
    show("2026-13-01");        // checker doesn't validate range
    show("abcd-05-19");        // non-digits
    show("2026-05-XX");
    show("2026-5-19");         // single-digit month
    return 0;
}`,
			// 2026-13-01 still PARSES (caller uses .is_valid() to
			// catch out-of-range month); the others all reject.
			wantStdout: "rejected \nrejected 2026/05/19\nrejected 2026-05-19T00\nACCEPTED 2026-13-01\nrejected abcd-05-19\nrejected 2026-05-XX\nrejected 2026-5-19\n",
		},
		{
			name: "Instant.format_rfc3339 emits canonical Z form",
			source: `import "std/time";
function main(): i32 {
    // Epoch.
    print((Instant { sec: 0 as i64, nsec: 0 }).format_rfc3339());
    // 2025-01-01T00:00:00Z: epoch sec = 55 years * 365.25 * 86400 ≈ 1735689600.
    print((Instant { sec: 1735689600 as i64, nsec: 0 }).format_rfc3339());
    // With ns fraction.
    print((Instant { sec: 0 as i64, nsec: 123456789 }).format_rfc3339());
    print((Instant { sec: 0 as i64, nsec: 1 }).format_rfc3339());
    // Pre-epoch (negative sec).
    print((Instant { sec: (0 as i64) - (1 as i64), nsec: 0 }).format_rfc3339());
    return 0;
}`,
			wantStdout: "1970-01-01T00:00:00Z\n2025-01-01T00:00:00Z\n1970-01-01T00:00:00.123456789Z\n1970-01-01T00:00:00.000000001Z\n1969-12-31T23:59:59Z\n",
		},
		{
			name: "instant_parse_rfc3339 round-trips through format",
			source: `import "std/time";
function show(s: string) {
    match (time.instant_parse_rfc3339(s)) {
        Some(p) => { print(p.format_rfc3339()); },
        None => { print("rejected"); }
    }
}
function main(): i32 {
    show("2026-05-19T14:30:00Z");
    show("1970-01-01T00:00:00Z");
    show("2024-02-29T23:59:59Z");
    show("2026-05-19T14:30:00.500Z");           // 3-digit fraction → nsec=5e8
    show("2026-05-19T14:30:00.123456789Z");
    return 0;
}`,
			wantStdout: "2026-05-19T14:30:00Z\n1970-01-01T00:00:00Z\n2024-02-29T23:59:59Z\n2026-05-19T14:30:00.500000000Z\n2026-05-19T14:30:00.123456789Z\n",
		},
		{
			name: "instant_parse_rfc3339 rejects malformed input",
			source: `import "std/time";
function show(s: string) {
    match (time.instant_parse_rfc3339(s)) {
        Some(_) => { print("ACCEPTED"); },
        None => { print("rejected"); }
    }
}
function main(): i32 {
    show("");
    show("2026-05-19");                          // date only
    show("2026-05-19T14:30:00");                  // missing Z
    show("2026-05-19t14:30:00z");                 // lowercase
    show("2026-05-19 14:30:00Z");                 // space instead of T
    show("2026-05-19T14:30:00+00:00");            // offset form (Phase 5)
    show("2026-05-19T14:30:00.Z");                // empty fraction
    show("2026-05-19T14:30:00.1234567890Z");      // 10-digit fraction
    return 0;
}`,
			wantStdout: "rejected\nrejected\nrejected\nrejected\nrejected\nrejected\nrejected\nrejected\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptZonedFixedOffset pins the Phase-5 fixed-
// offset zone surface (docs/STDLIB-DESIGN-RESEARCH.md Rec §4):
// TimeZone construction, Instant.in_zone, Zoned.to_datetime,
// Zoned.format_rfc3339, and the zoned RFC 3339 parser.
//
// IANA tzdb lookup (with DST transition tables) is a separate
// follow-up; these tests cover the fixed-offset path that
// every wasi-http handler running in UTC or a known constant
// offset needs today.
func TestInterpScriptZonedFixedOffset(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "timezone_fixed_offset emits canonical UTC±HH:MM names",
			source: `import "std/time";
function main(): i32 {
    print(time.timezone_fixed_offset(9 * 3600).name);
    print(time.timezone_fixed_offset(-5 * 3600).name);
    print(time.timezone_fixed_offset(0).name);
    print(time.timezone_fixed_offset(5 * 3600 + 30 * 60).name);   // India +05:30
    print(time.timezone_fixed_offset(-((9 * 3600) + (30 * 60))).name); // Marquesas -09:30
    return 0;
}`,
			wantStdout: "UTC+09:00\nUTC-05:00\nUTC+00:00\nUTC+05:30\nUTC-09:30\n",
		},
		{
			name: "Zoned shifts wall-clock by offset",
			source: `import "std/time";
function main(): i32 {
    // Pick an arbitrary UTC instant. The wall-clock at the
    // zone offsets is exactly UTC ± offset hours.
    var ts: Instant = Instant { sec: 1735689600 as i64, nsec: 0 };  // 2025-01-01T00:00:00Z
    print(ts.in_zone(time.timezone_utc()).format_rfc3339());
    print(ts.in_zone(time.timezone_fixed_offset(9 * 3600)).format_rfc3339());
    print(ts.in_zone(time.timezone_fixed_offset(-5 * 3600)).format_rfc3339());
    print(ts.in_zone(time.timezone_fixed_offset(0)).format_rfc3339());  // still Z, offset 0 collapses
    return 0;
}`,
			wantStdout: "2025-01-01T00:00:00Z\n2025-01-01T09:00:00+09:00\n2024-12-31T19:00:00-05:00\n2025-01-01T00:00:00Z\n",
		},
		{
			name: "Zoned format preserves nanoseconds",
			source: `import "std/time";
function main(): i32 {
    var ts: Instant = Instant { sec: 1735689600 as i64, nsec: 123456789 };
    print(ts.in_zone(time.timezone_fixed_offset(9 * 3600)).format_rfc3339());
    return 0;
}`,
			wantStdout: "2025-01-01T09:00:00.123456789+09:00\n",
		},
		{
			name: "Zoned round-trip parse / format",
			source: `import "std/time";
function show(s: string) {
    match (time.instant_zoned_parse_rfc3339(s)) {
        Some(z) => { print(z.format_rfc3339()); },
        None => { print("rejected"); }
    }
}
function main(): i32 {
    show("2026-05-19T14:30:00Z");
    show("2026-05-19T14:30:00+09:00");
    show("2026-05-19T14:30:00-05:00");
    show("2026-05-19T14:30:00+05:30");          // half-hour offset
    show("2026-05-19T14:30:00.500000000+09:00"); // with nsec
    return 0;
}`,
			wantStdout: "2026-05-19T14:30:00Z\n2026-05-19T14:30:00+09:00\n2026-05-19T14:30:00-05:00\n2026-05-19T14:30:00+05:30\n2026-05-19T14:30:00.500000000+09:00\n",
		},
		{
			name: "Parse computes correct UTC instant for an offset",
			source: `import "std/time";
function main(): i32 {
    // "2026-05-19T14:30:00+09:00" means wall-clock 14:30 in
    // a +9-hour zone, which is UTC 05:30 on the same date.
    // The equivalent UTC RFC 3339 string parses to the same
    // Instant.sec.
    match (time.instant_zoned_parse_rfc3339("2026-05-19T14:30:00+09:00")) {
        Some(zonedJp) => {
            match (time.instant_parse_rfc3339("2026-05-19T05:30:00Z")) {
                Some(utc) => {
                    if (zonedJp.instant.sec == utc.sec) { print("match"); } else { print("mismatch"); }
                },
                None => { print("utc parse fail"); }
            }
        },
        None => { print("jp parse fail"); }
    }
    return 0;
}`,
			wantStdout: "match\n",
		},
		{
			name: "Parser rejects malformed zoned input",
			source: `import "std/time";
function show(s: string) {
    match (time.instant_zoned_parse_rfc3339(s)) {
        Some(_) => { print("ACCEPTED"); },
        None => { print("rejected"); }
    }
}
function main(): i32 {
    show("2026-05-19T14:30:00");          // missing suffix
    show("2026-05-19T14:30:00+0900");     // no colon in offset
    show("2026-05-19T14:30:00+09");       // truncated offset
    show("2026-05-19T14:30:00+09:00X");   // trailing junk
    show("2026-05-19T14:30:00Z00:00");    // mixed Z + offset
    show("2026-05-19T14:30:00+ab:cd");    // non-digit offset
    return 0;
}`,
			wantStdout: "rejected\nrejected\nrejected\nrejected\nrejected\nrejected\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptSpanDurationArith pins the Phase-6 calendar-
// vs-absolute arithmetic split (docs/STDLIB-DESIGN-RESEARCH.md
// Rec §4): Span on Date snaps to month-end ("Jan 31 + 1 month
// = Feb 28/29"), Duration on Instant is straight sec/nsec
// arithmetic. They diverge precisely on month boundaries and
// (once Phase 5.x lands IANA tzdb) on DST transitions.
func TestInterpScriptSpanDurationArith(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "add_span months clamps day-end overflow",
			source: `import "std/time";
function show(d: Date) {
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
}
function main(): i32 {
    // Jan 31 + 1 month = Feb 28 (2026 non-leap) or Feb 29 (2024 leap).
    show(time.date_make(2026, 1, 31).add_span(time.span_months(1)));
    show(time.date_make(2024, 1, 31).add_span(time.span_months(1)));
    // Mar 31 + 1 month = Apr 30 (Apr has 30 days).
    show(time.date_make(2026, 3, 31).add_span(time.span_months(1)));
    // May 31 + 1 month = Jun 30. + 3 months = Aug 31 (Aug has 31).
    show(time.date_make(2026, 5, 31).add_span(time.span_months(1)));
    show(time.date_make(2026, 5, 31).add_span(time.span_months(3)));
    return 0;
}`,
			wantStdout: "2026-2-28\n2024-2-29\n2026-4-30\n2026-6-30\n2026-8-31\n",
		},
		{
			name: "add_span years clamps Feb 29 in non-leap target",
			source: `import "std/time";
function show(d: Date) {
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
}
function main(): i32 {
    // Feb 29 2024 + 1 year = Feb 28 2025 (non-leap).
    show(time.date_make(2024, 2, 29).add_span(time.span_years(1)));
    // Feb 29 2024 + 4 years = Feb 29 2028 (next leap).
    show(time.date_make(2024, 2, 29).add_span(time.span_years(4)));
    // Feb 29 2024 + 100 years = Feb 28 2124 (every 4th year is
    // leap unless century without 400 — 2124 is leap).
    show(time.date_make(2024, 2, 29).add_span(time.span_years(100)));
    return 0;
}`,
			wantStdout: "2025-2-28\n2028-2-29\n2124-2-29\n",
		},
		{
			name: "add_span composes months + days",
			source: `import "std/time";
function show(d: Date) {
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
}
function main(): i32 {
    show(time.date_make(2026, 1, 15).add_span(Span {
        years: 1, months: 2, weeks: 0, days: 5,
        hours: 0, minutes: 0, seconds: 0, nanos: 0,
    }));
    // Weeks + days add as serial-day offset (no clamping).
    show(time.date_make(2026, 3, 30).add_span(time.span_weeks(2)));
    // Mixed weeks + days.
    show(time.date_make(2026, 1, 1).add_span(Span {
        years: 0, months: 0, weeks: 1, days: 3,
        hours: 0, minutes: 0, seconds: 0, nanos: 0,
    }));
    return 0;
}`,
			wantStdout: "2027-3-20\n2026-4-13\n2026-1-11\n",
		},
		{
			name: "add_span with negative months walks backward",
			source: `import "std/time";
function show(d: Date) {
    print(d.year.to_string() + "-" + d.month.to_string() + "-" + d.day.to_string());
}
function main(): i32 {
    show(time.date_make(2026, 3, 15).add_span(time.span_months(-1)));
    show(time.date_make(2026, 1, 15).add_span(time.span_months(-1)));
    show(time.date_make(2026, 1, 15).add_span(time.span_months(-13)));
    return 0;
}`,
			wantStdout: "2026-2-15\n2025-12-15\n2024-12-15\n",
		},
		{
			name: "Instant.add_duration handles nsec carry",
			source: `
import "std/i64";
import "std/time";
function main(): i32 {
    // 1000.5 + 5.75 = 1006.25 (carry: 500e6 + 750e6 = 1.25e9 → +1 sec, 250e6 ns).
    var t1: Instant = Instant { sec: 1000 as i64, nsec: 500000000 };
    var sum: Instant = t1.add_duration(Duration { sec: 5 as i64, nsec: 750000000 });
    print(sum.sec.to_string() + "." + sum.nsec.to_string());

    // Exact 1-second carry: 999_999_999 + 1 = 1_000_000_000 → +1 sec.
    var edge: Instant = (Instant { sec: 0 as i64, nsec: 999999999 }).add_duration(Duration { sec: 0 as i64, nsec: 1 });
    print(edge.sec.to_string() + "." + edge.nsec.to_string());
    return 0;
}`,
			wantStdout: "1006.250000000\n1.0\n",
		},
		{
			name: "Instant.add_duration with negative shifts backward",
			source: `
import "std/i64";
import "std/time";
function main(): i32 {
    var t: Instant = Instant { sec: 1000 as i64, nsec: 0 };
    var back: Instant = t.add_duration(Duration { sec: (0 as i64) - (10 as i64), nsec: 0 });
    print(back.sec.to_string());
    return 0;
}`,
			wantStdout: "990\n",
		},
		{
			name: "Instant.duration_since computes signed delta with borrow",
			source: `
import "std/i64";
import "std/time";
function main(): i32 {
    var a: Instant = Instant { sec: 1000 as i64, nsec: 200000000 };
    var b: Instant = Instant { sec: 1005 as i64, nsec: 700000000 };
    // b - a = 5.5s
    var fwd: Duration = b.duration_since(a);
    print(fwd.sec.to_string() + "+" + fwd.nsec.to_string());
    // Borrow case: a - b should produce nsec >= 0 by adjustment.
    // a.nsec(200M) - b.nsec(700M) = -500M → borrow 1 sec, nsec = 500M.
    var rev: Duration = a.duration_since(b);
    print(rev.sec.to_string() + "+" + rev.nsec.to_string());
    return 0;
}`,
			wantStdout: "5+500000000\n-6+500000000\n",
		},
		{
			name: "days_until is the Span counterpart to days_since",
			source: `import "std/time";
function main(): i32 {
    var a: Date = time.date_make(2026, 1, 1);
    var b: Date = time.date_make(2026, 12, 31);
    var s: Span = a.days_until(b);
    print(s.days.to_string());          // 364
    print(b.days_until(a).days.to_string());  // -364
    return 0;
}`,
			wantStdout: "364\n-364\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptTimezoneIana pins the Phase-5.x IANA-name
// lookup (docs/STDLIB-DESIGN-RESEARCH.md Rec §4). Returns a
// fixed-offset TimeZone for the zone's standard-time offset
// — DST is not modeled yet, so summer-time wall-clock values
// for affected zones will be wrong; the table covers the
// ~80% of edge-handler workloads where UTC-anchored
// timestamp stamping is what's needed.
func TestInterpScriptTimezoneIana(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "UTC aliases all map to offset zero",
			source: `import "std/time";
function show(zone_name: string) {
    match (time.timezone_iana(zone_name)) {
        Some(tz) => { print(zone_name + " -> " + tz.name); },
        None => { print(zone_name + " -> none"); }
    }
}
function main(): i32 {
    show("UTC");
    show("GMT");
    show("Z");
    show("Etc/UTC");
    show("Etc/GMT");
    return 0;
}`,
			wantStdout: "UTC -> UTC+00:00\nGMT -> UTC+00:00\nZ -> UTC+00:00\nEtc/UTC -> UTC+00:00\nEtc/GMT -> UTC+00:00\n",
		},
		{
			name: "North American zones map to standard-time offsets",
			source: `import "std/time";
function show(zone_name: string) {
    match (time.timezone_iana(zone_name)) {
        Some(tz) => { print(zone_name + " -> " + tz.offset_seconds.to_string()); },
        None => { print(zone_name + " -> none"); }
    }
}
function main(): i32 {
    show("America/New_York");      // EST = -5h
    show("America/Chicago");       // CST = -6h
    show("America/Denver");        // MST = -7h
    show("America/Los_Angeles");   // PST = -8h
    show("America/Anchorage");     // AKST = -9h
    show("America/Honolulu");      // HST = -10h
    return 0;
}`,
			wantStdout: "America/New_York -> -18000\nAmerica/Chicago -> -21600\nAmerica/Denver -> -25200\nAmerica/Los_Angeles -> -28800\nAmerica/Anchorage -> -32400\nAmerica/Honolulu -> -36000\n",
		},
		{
			name: "Asia zones cover half-hour offset for India",
			source: `import "std/time";
function show(zone_name: string) {
    match (time.timezone_iana(zone_name)) {
        Some(tz) => { print(zone_name + " -> " + tz.name); },
        None => { print(zone_name + " -> none"); }
    }
}
function main(): i32 {
    show("Asia/Tokyo");
    show("Asia/Shanghai");
    show("Asia/Hong_Kong");
    show("Asia/Singapore");
    show("Asia/Kolkata");
    show("Asia/Dubai");
    show("Asia/Bangkok");
    return 0;
}`,
			wantStdout: "Asia/Tokyo -> UTC+09:00\nAsia/Shanghai -> UTC+08:00\nAsia/Hong_Kong -> UTC+08:00\nAsia/Singapore -> UTC+08:00\nAsia/Kolkata -> UTC+05:30\nAsia/Dubai -> UTC+04:00\nAsia/Bangkok -> UTC+07:00\n",
		},
		{
			name: "Unknown zone names return None",
			source: `import "std/time";
function main(): i32 {
    match (time.timezone_iana("Mars/Olympus_Mons")) {
        Some(_) => { print("ACCEPTED"); return 1; },
        None => { print("none"); }
    }
    match (time.timezone_iana("")) {
        Some(_) => { return 2; },
        None => { print("empty-none"); }
    }
    match (time.timezone_iana("america/new_york")) {  // wrong case
        Some(_) => { return 3; },
        None => { print("case-none"); }
    }
    return 0;
}`,
			wantStdout: "none\nempty-none\ncase-none\n",
		},
		{
			name: "IANA zone integrates with Instant.in_zone",
			source: `import "std/time";
function main(): i32 {
    // 2025-01-01T00:00:00Z + Asia/Tokyo (+9h) = 2025-01-01T09:00:00+09:00.
    match (time.timezone_iana("Asia/Tokyo")) {
        Some(jp) => {
            var ts: Instant = Instant { sec: 1735689600 as i64, nsec: 0 };
            print(ts.in_zone(jp).format_rfc3339());
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "2025-01-01T09:00:00+09:00\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptHttpRequestBodyShim pins the Tier-A Rec §1
// forward-compatibility shim
// (docs/STDLIB-DESIGN-RESEARCH.md). Handler code uses
// `req.body_string()` / `req.body_bytes()` / `req.body_len()`
// instead of accessing `req.body` directly so the future
// Stream[bytes] field migration is invisible to callers.
func TestInterpScriptHttpRequestBodyShim(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := `import "std/http";

function main(): i32 {
    var wire: string = "POST /upload HTTP/1.1\r\nContent-Length: 11\r\n\r\nhello world";
    match (http.http_parse_request(wire)) {
        Some(req) => {
            if (req.body_string() != "hello world") { return 1; }
            if (req.body_len() != 11) { return 2; }
            var bs: u8[] = req.body_bytes();
            if (bs.len() != 11) { return 3; }
            if (bs[0] != 104) { return 4; }  // 'h'
            if (bs[10] != 100) { return 5; } // 'd'
            return 0;
        },
        None => { return 99; }
    }
    return 0;
}`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
}

// TestInterpScriptStreamPrimitive pins the Phase-1 Stream
// surface (docs/STDLIB-DESIGN-RESEARCH.md Rec §1 Phase 2).
// Stream is the eventual home for HttpRequest.body; today's
// in-memory buffer-backed shape gives handler code the
// future API surface immediately.
func TestInterpScriptStreamPrimitive(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "stream_from_string + read_all_string round-trips",
			source: `import "std/stream";
function main(): i32 {
    var s: Stream = stream.stream_from_string("hello world");
    print(s.len().to_string());
    var (text, s2) = s.read_all_string();
    print(text);
    if (s2.is_empty()) { print("done"); }
    return 0;
}`,
			wantStdout: "11\nhello world\ndone\n",
		},
		{
			name: "stream_from_bytes + read_all returns the byte buffer",
			source: `import "std/stream";
function main(): i32 {
    var bs: u8[] = "abc".bytes();
    var s: Stream = stream.stream_from_bytes(bs);
    var (got, s2) = s.read_all();
    print(got.len().to_string());
    print((got[0] as i32).to_string());
    print((got[2] as i32).to_string());
    return 0;
}`,
			wantStdout: "3\n97\n99\n",
		},
		{
			name: "remaining decrements as bytes are consumed",
			source: `import "std/stream";
function main(): i32 {
    var s: Stream = stream.stream_from_string("abc");
    print(s.remaining().to_string());
    var (_, s2) = s.read_all();
    // cursor idiom: the advanced Stream is s2; original s is unchanged.
    print(s2.remaining().to_string());
    print(s2.len().to_string());
    return 0;
}`,
			wantStdout: "3\n0\n3\n",
		},
		{
			name: "stream_empty is consistent",
			source: `import "std/stream";
function main(): i32 {
    var s: Stream = stream.stream_empty();
    if (s.len() != 0) { return 1; }
    if (s.remaining() != 0) { return 2; }
    if (!s.is_empty()) { return 3; }
    var (bs, s2) = s.read_all();
    if (bs.len() != 0) { return 4; }
    print("ok");
    return 0;
}`,
			wantStdout: "ok\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptMockPlatform pins the Tier-C Rec §11
// test-ergonomics surface (docs/PLATFORM-RESEARCH.md §6).
// Phase 1 mocks are manually driven (tests call .record()
// themselves); Phase 2 will integrate with Platform's
// capability fields so the mock intercepts calls
// automatically.
func TestInterpScriptMockPlatform(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "record + call_count + indexed access",
			source: `
import "std/i32";
import "std/mock_platform";
function main(): i32 {
    var m: MockPlatform = mock_platform.mock_platform_new();
    m = m.record("fetch", "GET /users/42");
    m = m.record("kv_set", "user:42=Alice");
    print(m.call_count().to_string());
    print(m.calls[0].name);
    print(m.calls[1].args);
    return 0;
}`,
			wantStdout: "2\nfetch\nuser:42=Alice\n",
		},
		{
			name: "has_call distinguishes present / absent",
			source: `import "std/mock_platform";
function main(): i32 {
    var m: MockPlatform = mock_platform.mock_platform_new();
    m = m.record("fetch", "x");
    if (m.has_call("fetch")) { print("yes-fetch"); } else { print("no-fetch"); }
    if (m.has_call("write_file")) { print("yes-wf"); } else { print("no-wf"); }
    return 0;
}`,
			wantStdout: "yes-fetch\nno-wf\n",
		},
		{
			name: "find_call returns Some/None correctly",
			source: `import "std/mock_platform";
function main(): i32 {
    var m: MockPlatform = mock_platform.mock_platform_new();
    m = m.record("fetch", "first");
    m = m.record("kv_set", "second");
    m = m.record("fetch", "third");
    // find_call returns the FIRST match.
    match (m.find_call("fetch")) {
        Some(c) => { print(c.args); },
        None => { print("missing"); }
    }
    match (m.find_call("unknown")) {
        Some(_) => { print("FOUND"); },
        None => { print("none"); }
    }
    return 0;
}`,
			wantStdout: "first\nnone\n",
		},
		{
			name: "reset clears the log",
			source: `import "std/mock_platform";
function main(): i32 {
    var m: MockPlatform = mock_platform.mock_platform_new();
    m = m.record("a", "1");
    m = m.record("b", "2");
    if (m.call_count() != 2) { return 1; }
    m = m.reset();
    if (m.call_count() != 0) { return 2; }
    if (m.has_call("a")) { return 3; }
    print("ok");
    return 0;
}`,
			wantStdout: "ok\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptJsonGetters pins the Tier-C Rec §12 Phase 1
// surface (docs/STDLIB-DESIGN-RESEARCH.md Rec §3). Schema-
// directed `json_parse[T]` codegen is multi-week; Phase 1
// ships typed-field extraction helpers that handler code
// uses to walk a JsonValue DOM without manually pattern-
// matching every level.
func TestInterpScriptJsonGetters(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "json_get_string returns Some on match, None on miss",
			source: `import "std/json";
function main(): i32 {
    match (json.json_parse("{\"name\":\"Alice\"}")) {
        Some(v) => {
            match (json.json_get_string(v, "name")) {
                Some(n) => { print(n); },
                None => { print("none"); }
            }
            match (json.json_get_string(v, "missing")) {
                Some(_) => { print("FOUND"); },
                None => { print("absent"); }
            }
            return 0;
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "Alice\nabsent\n",
		},
		{
			name: "json_get_i32 parses positive + negative integers",
			source: `import "std/json";
function main(): i32 {
    match (json.json_parse("{\"a\":42,\"b\":-7,\"c\":0}")) {
        Some(v) => {
            match (json.json_get_i32(v, "a")) { Some(n) => { print(n.to_string()); }, None => { print("none"); } }
            match (json.json_get_i32(v, "b")) { Some(n) => { print(n.to_string()); }, None => { print("none"); } }
            match (json.json_get_i32(v, "c")) { Some(n) => { print(n.to_string()); }, None => { print("none"); } }
            return 0;
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "42\n-7\n0\n",
		},
		{
			name: "json_get_bool extracts true / false correctly",
			source: `import "std/json";
function main(): i32 {
    match (json.json_parse("{\"yes\":true,\"no\":false}")) {
        Some(v) => {
            match (json.json_get_bool(v, "yes")) {
                Some(b) => { if (b) { print("YES"); } else { print("no"); } },
                None => { print("none"); }
            }
            match (json.json_get_bool(v, "no")) {
                Some(b) => { if (b) { print("YES"); } else { print("no"); } },
                None => { print("none"); }
            }
            return 0;
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "YES\nno\n",
		},
		{
			name: "json_get_array returns the element list",
			source: `import "std/json";
function main(): i32 {
    match (json.json_parse("{\"tags\":[\"a\",\"b\",\"c\"]}")) {
        Some(v) => {
            match (json.json_get_array(v, "tags")) {
                Some(arr) => { print(arr.len().to_string()); },
                None => { print("none"); }
            }
            return 0;
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "3\n",
		},
		{
			name: "json_get_object chains into nested objects",
			source: `import "std/json";
function main(): i32 {
    match (json.json_parse("{\"user\":{\"name\":\"Bob\"}}")) {
        Some(v) => {
            match (json.json_get_object(v, "user")) {
                Some(inner) => {
                    match (json.json_get_string(inner, "name")) {
                        Some(n) => { print(n); },
                        None => { print("none"); }
                    }
                },
                None => { print("none"); }
            }
            return 0;
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "Bob\n",
		},
		{
			name: "type mismatch returns None (not the wrong-shaped value)",
			source: `import "std/json";
function main(): i32 {
    match (json.json_parse("{\"age\":\"forty\"}")) {
        Some(v) => {
            // age is a string in the JSON; json_get_i32 must return None.
            match (json.json_get_i32(v, "age")) {
                Some(_) => { print("WRONG"); },
                None => { print("rejected"); }
            }
            return 0;
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "rejected\n",
		},
		{
			name: "json_is_null distinguishes null from absent / non-null",
			source: `import "std/json";
function main(): i32 {
    match (json.json_parse("{\"x\":null,\"y\":1}")) {
        Some(v) => {
            match (json.json_get(v, "x")) {
                Some(xv) => { if (json.json_is_null(xv)) { print("null"); } else { print("not null"); } },
                None => { print("missing"); }
            }
            match (json.json_get(v, "y")) {
                Some(yv) => { if (json.json_is_null(yv)) { print("null"); } else { print("not null"); } },
                None => { print("missing"); }
            }
            return 0;
        },
        None => { return 1; }
    }
    return 0;
}`,
			wantStdout: "null\nnot null\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptStreamReader pins the Tier-C Rec §13 Phase 1
// Reader-shape methods on Stream (docs/STDLIB-DESIGN-RESEARCH.md
// Rec §5).
func TestInterpScriptStreamReader(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "read_byte returns Some until exhausted",
			source: `import "std/stream";
function main(): i32 {
    var s: Stream = stream.stream_from_string("ab");
    var (b1, s2) = s.read_byte();
    match (b1) { Some(b) => { print(b.to_string()); }, None => { print("none"); } }
    var (b2, s3) = s2.read_byte();
    match (b2) { Some(b) => { print(b.to_string()); }, None => { print("none"); } }
    var (b3, _) = s3.read_byte();
    match (b3) { Some(_) => { print("UNEXPECTED"); }, None => { print("none"); } }
    return 0;
}`,
			wantStdout: "97\n98\nnone\n",
		},
		{
			name: "read_n caps at available bytes",
			source: `import "std/stream";
function main(): i32 {
    var s: Stream = stream.stream_from_string("hello");
    var (first, s2) = s.read_n(3);
    print(first.len().to_string());
    var (rest, s3) = s2.read_n(99);
    print(rest.len().to_string());
    var (empty, _) = s3.read_n(1);
    print(empty.len().to_string());
    return 0;
}`,
			wantStdout: "3\n2\n0\n",
		},
		{
			name: "read_line strips both LF and CRLF",
			source: `import "std/stream";
function main(): i32 {
    var s: Stream = stream.stream_from_string("unix\nwindows\r\nfinal");
    var (l1, s2) = s.read_line();
    match (l1) { Some(l) => { print(l); }, None => { print("none"); } }
    var (l2, s3) = s2.read_line();
    match (l2) { Some(l) => { print(l); }, None => { print("none"); } }
    var (l3, s4) = s3.read_line();
    match (l3) { Some(l) => { print(l); }, None => { print("none"); } }
    var (l4, _) = s4.read_line();
    match (l4) { Some(_) => { print("UNEXPECTED"); }, None => { print("eof"); } }
    return 0;
}`,
			wantStdout: "unix\nwindows\nfinal\neof\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
					got, tc.wantStdout, errb.String())
			}
		})
	}
}

// TestInterpScriptBytesWriter pins the in-memory writer
// (docs/STDLIB-DESIGN-RESEARCH.md Rec §5's MemoryWriter).
func TestInterpScriptBytesWriter(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cases := []struct {
		name, source, wantStdout string
	}{
		{
			name: "write_string + into_string round-trips",
			source: `import "std/io_buffered";
function main(): i32 {
    var w: BytesWriter = io_buffered.bytes_writer_new();
    w = w.write_string("HTTP/1.1 200 OK\r\n");
    w = w.write_string("\r\nhello");
    print(w.len().to_string());
    print(w.into_string());
    return 0;
}`,
			wantStdout: "24\nHTTP/1.1 200 OK\r\n\r\nhello\n",
		},
		{
			name: "write_byte appends single bytes",
			source: `import "std/io_buffered";
function main(): i32 {
    var w: BytesWriter = io_buffered.bytes_writer_new();
    w = w.write_byte(72);  // 'H'
    w = w.write_byte(105); // 'i'
    print(w.into_string());
    return 0;
}`,
			wantStdout: "Hi\n",
		},
		{
			name: "reset clears the buffer for reuse",
			source: `import "std/io_buffered";
function main(): i32 {
    var w: BytesWriter = io_buffered.bytes_writer_new();
    w = w.write_string("first");
    if (w.len() != 5) { return 1; }
    w = w.reset();
    if (w.len() != 0) { return 2; }
    if (!w.is_empty()) { return 3; }
    w = w.write_string("second");
    print(w.into_string());
    return 0;
}`,
			wantStdout: "second\n",
		},
		{
			name: "write_bytes for raw u8[] payloads",
			source: `import "std/io_buffered";
function main(): i32 {
    var w: BytesWriter = io_buffered.bytes_writer_new();
    var bs: u8[] = "binary".bytes();
    w = w.write_bytes(bs);
    if (w.len() != 6) { return 1; }
    print(w.into_string());
    return 0;
}`,
			wantStdout: "binary\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
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
			if got := out.String(); got != tc.wantStdout {
				t.Errorf("stdout = %q,\n want %q\nstderr: %s",
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
