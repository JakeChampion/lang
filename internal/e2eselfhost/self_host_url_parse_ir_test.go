package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// urlParseIRCases exercise std/url's `url_parse` — decomposing a URL into a
// 6-field struct — through the self-host IR path on x86-64 + wasm (extending the
// url_codec audit). The single-program driver resolves no imports and `Url` is a
// reserved builtin name, so the struct is inlined as `Uri` and a `field` helper
// reads a chosen component; this verifies the constructs `url_parse` lowers to
// compile on the IR path: a 6-field struct with mixed string + i32 fields,
// repeated functional struct-spread updates (`Uri { ...u, host: …, port: … }`),
// string slicing, byte scanning, and `Option[Uri]` `Some`/`None` returned and
// read via a payload-binding `match`. Each program returns a small deterministic
// int (kept <= 126), pinned to the `"ir"` path; expectations are hardcoded,
// verified against the native interp + x86-64 backends. FEATURE-AUDIT std/url row.
const urlParseIRPrelude = `struct Uri { scheme: string, host: string, port: i32, path: string, query: string, fragment: string }
function uri_parse(s: string): Option[Uri] {
    var n: i32 = s.len();
    if (n == 0) { return None; }
    var u: Uri = Uri { scheme: "", host: "", port: 0, path: "", query: "", fragment: "" };
    var scheme_end: i32 = -1;
    var i: i32 = 0;
    while (i + 2 < n) {
        if (s[i] == 58 && s[i+1] == 47 && s[i+2] == 47) { if (i > 0) { scheme_end = i; } break; }
        i = i + 1;
    }
    var rest_start: i32 = 0;
    if (scheme_end >= 0) { u = Uri { ...u, scheme: s[0:scheme_end] }; rest_start = scheme_end + 3; }
    var frag_start: i32 = n;
    i = rest_start;
    while (i < n) { if (s[i] == 35) { frag_start = i; break; } i = i + 1; }
    if (frag_start < n) { u = Uri { ...u, fragment: s[frag_start+1:n] }; }
    var query_start: i32 = frag_start;
    i = rest_start;
    while (i < frag_start) { if (s[i] == 63) { query_start = i; break; } i = i + 1; }
    if (query_start < frag_start) { u = Uri { ...u, query: s[query_start+1:frag_start] }; }
    var authority_end: i32 = query_start;
    if (scheme_end >= 0) {
        i = rest_start;
        while (i < query_start) { if (s[i] == 47) { authority_end = i; break; } i = i + 1; }
    } else { authority_end = rest_start; }
    if (rest_start < authority_end) {
        var colon: i32 = authority_end;
        i = rest_start;
        while (i < authority_end) { if (s[i] == 58) { colon = i; break; } i = i + 1; }
        if (colon < authority_end) {
            var port: i32 = 0;
            i = colon + 1;
            while (i < authority_end) { var b: i32 = s[i] as i32; if (b < 48 || b > 57) { port = 0; break; } port = port * 10 + (b - 48); i = i + 1; }
            u = Uri { ...u, host: s[rest_start:colon], port: port };
        } else { u = Uri { ...u, host: s[rest_start:authority_end] }; }
    }
    u = Uri { ...u, path: s[authority_end:query_start] };
    return Some(u);
}
function field(s: string, which: i32): i32 {
    match (uri_parse(s)) {
        Some(u) => {
            if (which == 0) { return u.host.len(); }
            if (which == 1) { return u.port; }
            if (which == 2) { return u.path.len(); }
            if (which == 3) { return u.fragment.len(); }
            if (which == 4) { return u.query.len(); }
            return u.scheme.len();
        },
        None => { return 99; },
    }
    return 99;
}
`

var urlParseIRCases = []struct {
	name string
	main string
	want int
}{
	// "http://h.com:8080/p?q#f": scheme "http" (4).
	{"scheme-len", `return field("http://h.com:8080/p?q#f", 5);`, 4},
	// host "h.com" (5).
	{"host-len", `return field("http://h.com:8080/p?q#f", 0);`, 5},
	// port 8080 (returned minus 8000 to stay in exit-code range).
	{"port", `return field("http://h.com:8080/p?q#f", 1) - 8000;`, 80},
	// path "/p" (2).
	{"path-len", `return field("http://h.com:8080/p?q#f", 2);`, 2},
	// query "q" (1) + fragment "f" (1) -> 2.
	{"query-and-frag", `return field("http://h.com:8080/p?q#f", 4) + field("http://h.com:8080/p?q#f", 3);`, 2},
	// no scheme: the whole input is the path -> "/a/b" (4).
	{"no-scheme-path", `return field("/a/b", 2);`, 4},
	// empty input -> None -> 99.
	{"empty-none", `return field("", 0);`, 99},
	// a non-digit in the port zeroes it -> 0.
	{"bad-port", `return field("http://x:9z9/", 1);`, 0},
}

func urlParseIRSrc(mainBody string) string {
	return urlParseIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostUrlParseIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, pinned to the "ir" path.
func TestSelfHostUrlParseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range urlParseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(urlParseIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostUrlParseIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostUrlParseIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host url-parse wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range urlParseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(urlParseIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "urlparse_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("url-parse wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
