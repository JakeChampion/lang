package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// urlQueryIRCases exercise std/url's `query_parse` — parsing a query string into
// a `Map[string, string[]]` (duplicate keys accumulate) — through the self-host
// IR path on x86-64 + wasm, completing the std/url audit (after url_codec +
// url_parse). The single-program driver resolves no imports and treats `Map { }`
// + `string_from_bytes_unchecked` as self-host builtins, so `query_parse` + `url_decode`
// are inlined; this verifies the constructs it lowers to compile on the IR path:
// a `Map[string, string[]]` (string keys, string-ARRAY values) built via
// `Map {}` / `.get` / `.insert`, the append-or-create idiom over the map's
// `string[]` value, `Option[string[]]` `Some`/`None` `match`, `url_decode`'s
// `u8[]` + `string_from_bytes_unchecked`, and byte scanning. Each program returns a small
// deterministic int (kept <= 126), pinned to the `"ir"` path; expectations are
// hardcoded, verified against the native interp + x86-64 backends. The
// `dup-keys` case (append to an existing `string[]` map value) is the #3495
// regression guard: it previously corrupted a sibling key's array on the wasm
// IR backend (returning 22 not 21) because `op_map_set` left the wasm `vis`
// RC-retain flag at 0 for a pointer value; fixed by threading value-pointerness
// through `op_map_set`. FEATURE-AUDIT std/url row.
const urlQueryIRPrelude = `function url_hex_val(c: i32): i32 {
    if (c >= 48 && c <= 57) { return c - 48; }
    if (c >= 97 && c <= 102) { return c - 87; }
    if (c >= 65 && c <= 70) { return c - 55; }
    return -1;
}
function url_decode(s: string): string {
    var n: i32 = s.len();
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        var b: i32 = s[i] as i32;
        var emit: string = s[i:i+1];
        var consumed: i32 = 1;
        if (b == 37 && i + 2 < n) {
            var h1: i32 = url_hex_val(s[i+1]);
            var h2: i32 = url_hex_val(s[i+2]);
            if (h1 >= 0 && h2 >= 0) { var by: u8[] = [((h1 << 4) | h2) as u8]; emit = string_from_bytes_unchecked(by); consumed = 3; }
        }
        out = out + emit;
        i = i + consumed;
    }
    return out;
}
function append_pair(m: Map[string, string[]], k: string, v: string): Map[string, string[]] {
    match (m.get(k)) {
        Some(existing) => { return m.insert(k, existing.append(v)); },
        None => { var arr: string[] = [v]; return m.insert(k, arr); },
    }
    return m;
}
function query_parse(s: string): Map[string, string[]] {
    var m: Map[string, string[]] = Map {};
    var n: i32 = s.len();
    if (n == 0) { return m; }
    var pair_start: i32 = 0;
    var i: i32 = 0;
    while (i <= n) {
        var sep: boolean = false;
        if (i == n) { sep = true; } else if (s[i] == 38) { sep = true; }
        if (sep) {
            if (i - pair_start > 0) {
                var eq: i32 = -1;
                var j: i32 = pair_start;
                while (j < i) { if (s[j] == 61) { eq = j; break; } j = j + 1; }
                if (eq >= 0) { m = append_pair(m, url_decode(s[pair_start:eq]), url_decode(s[eq+1:i])); }
                else { m = append_pair(m, url_decode(s[pair_start:i]), ""); }
            }
            pair_start = i + 1;
        }
        i = i + 1;
    }
    return m;
}
function vcount(m: Map[string, string[]], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v.len(); }, None => { return 0; }, }
    return 0;
}
function v0len(m: Map[string, string[]], k: string): i32 {
    match (m.get(k)) { Some(v) => { return v[0].len(); }, None => { return 0; }, }
    return 0;
}
`

var urlQueryIRCases = []struct {
	name string
	main string
	want int
}{
	// duplicate-key accumulation: "a" -> [1,3] (2), "b" -> [2] (1) -> 2*10+1.
	// #3495 regression guard (pre-fix the wasm IR backend returned 22 — b's
	// array corrupted by the append to a — because map_set's value `vis` flag
	// was hardcoded 0 for the string[] value).
	{"dup-keys", `var m: Map[string, string[]] = query_parse("a=1&b=2&a=3"); return vcount(m, "a") * 10 + vcount(m, "b");`, 21},
	// single value.
	{"single", `return vcount(query_parse("x=hello"), "x");`, 1},
	// missing key -> the None arm -> 0.
	{"missing", `return vcount(query_parse("a=1"), "zzz");`, 0},
	// a bare key (no '=') stores one empty-string value.
	{"flag-no-value", `return vcount(query_parse("flag"), "flag");`, 1},
	// empty query -> empty map -> 0.
	{"empty", `return vcount(query_parse(""), "a");`, 0},
	// percent-decoded key: "a%20b" -> "a b".
	{"decoded-key", `return vcount(query_parse("a%20b=c"), "a b");`, 1},
	// percent-decoded value: "c%2Fd" -> "c/d" (len 3).
	{"decoded-value", `return v0len(query_parse("k=c%2Fd"), "k");`, 3},
}

func urlQueryIRSrc(mainBody string) string {
	return urlQueryIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostUrlQueryIRX86_64 routes each case through the self-hosted x86-64 IR
// driver, pinned to the "ir" path.
func TestSelfHostUrlQueryIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range urlQueryIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(urlQueryIRSrc(tc.main))
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

// TestSelfHostUrlQueryIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostUrlQueryIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host url-query wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range urlQueryIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(urlQueryIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "urlquery_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("url-query wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
