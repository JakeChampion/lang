package e2eselfhost

import "testing"

// urlQueryIRCases exercise std/url's `query_parse` — parsing a query string
// into a `Map[string, string[]]` (duplicate keys accumulate) — through the
// self-host IR path on x86-64 + wasm, completing the std/url audit (after
// url_codec + url_parse). `query_parse` + `url_decode` are inlined rather than
// imported; this verifies the constructs it lowers to compile on the IR path: a
// `Map[string, string[]]` (string keys, string-ARRAY values) built via `Map {}`
// / `.get` / `.insert`, the append-or-create idiom over the map's `string[]`
// value, `Option[string[]]` `Some`/`None` `match`, `url_decode`'s `u8[]` +
// `string_from_bytes_unchecked`, and byte scanning. Each program returns a
// small deterministic int (kept <= 126); expectations are hardcoded, verified
// against the native interp + x86-64 backends. The `dup-keys` case (append to
// an existing `string[]` map value) is the #3495 regression guard: it corrupts
// a sibling key's array on the wasm IR backend (returning 22 not 21) if
// `op_map_set` leaves the wasm `vis` RC-retain flag at 0 for a pointer value;
// fixed by threading value-pointerness through `op_map_set`. FEATURE-AUDIT
// std/url row.
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
        var emit: string = slice_unchecked(s, i, i+1).to_owned();
        var consumed: i32 = 1;
        if (b == 37 && i + 2 < n) {
            var h1: i32 = url_hex_val(s[i+1] as i32);
            var h2: i32 = url_hex_val(s[i+2] as i32);
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
                if (eq >= 0) { m = append_pair(m, url_decode(slice_unchecked(s, pair_start, eq).to_owned()), url_decode(slice_unchecked(s, eq+1, i).to_owned())); }
                else { m = append_pair(m, url_decode(slice_unchecked(s, pair_start, i).to_owned()), ""); }
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
	return "import \"core/map\";\nimport \"std/string\";\n" + urlQueryIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostUrlQueryIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostUrlQueryIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range urlQueryIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, urlQueryIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
