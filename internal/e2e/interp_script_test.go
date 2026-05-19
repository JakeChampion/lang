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
            return req.headers.size();
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
            while (i < len(all)) {
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
    r.headers.set("X-Trace-Id", "abc123");
    r.headers.set("Cache-Control", "no-store");
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
    r.headers.set("Content-Length", "9999");
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
    r.headers.append("Set-Cookie", "a=1");
    r.headers.append("Set-Cookie", "b=2");
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
			source: `import "std/time";
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
			source: `import "std/time";
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

// TestInterpScriptInstantNow pins `std/time.instant_now()`
// (docs/STDLIB-DESIGN-RESEARCH.md Rec §4 Phase 2). Reads the
// current wall-clock time and asserts the seconds field is
// in a plausible range — past a sentinel epoch second and
// before a far-future bound. Catches accidental sign / unit
// mistakes without baking in a brittle exact value.
func TestInterpScriptInstantNow(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := `import "std/time";

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
	srcPath := filepath.Join(dir, "prog.lang")
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
