package e2ecompiler

import "testing"

const urlQueryIRPrelude = `function url_hex_val(c: i32): i32 {
    if (c >= 48 && c <= 57) { return c - 48; }
    if (c >= 97 && c <= 102) { return c - 87; }
    if (c >= 65 && c <= 70) { return c - 55; }
    return -1;
}
function url_decode(s: string): string {
    let n: i32 = s.len();
    let out: string = "";
    let i: i32 = 0;
    while (i < n) {
        let b: i32 = s[i] as i32;
        let emit: string = slice_unchecked(s, i, i+1).to_owned();
        let consumed: i32 = 1;
        if (b == 37 && i + 2 < n) {
            let h1: i32 = url_hex_val(s[i+1] as i32);
            let h2: i32 = url_hex_val(s[i+2] as i32);
            if (h1 >= 0 && h2 >= 0) { let by: u8[] = [((h1 << 4) | h2) as u8]; emit = string_from_bytes_unchecked(by); consumed = 3; }
        }
        out = out + emit;
        i = i + consumed;
    }
    return out;
}
function append_pair(m: Map[string, string[]], k: string, v: string): Map[string, string[]] {
    match (m.get(k)) {
        Some(existing) => { return m.insert(k, existing.append(v)); },
        None => { let arr: string[] = [v]; return m.insert(k, arr); },
    }
    return m;
}
function query_parse(s: string): Map[string, string[]] {
    let m: Map[string, string[]] = Map {};
    let n: i32 = s.len();
    if (n == 0) { return m; }
    let pair_start: i32 = 0;
    let i: i32 = 0;
    while (i <= n) {
        let sep: boolean = false;
        if (i == n) { sep = true; } else if (s[i] == 38) { sep = true; }
        if (sep) {
            if (i - pair_start > 0) {
                let eq: i32 = -1;
                let j: i32 = pair_start;
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
	{"dup-keys", `let m: Map[string, string[]] = query_parse("a=1&b=2&a=3"); return vcount(m, "a") * 10 + vcount(m, "b");`, 21},
	{"single", `return vcount(query_parse("x=hello"), "x");`, 1},
	{"missing", `return vcount(query_parse("a=1"), "zzz");`, 0},
	{"flag-no-value", `return vcount(query_parse("flag"), "flag");`, 1},
	{"empty", `return vcount(query_parse(""), "a");`, 0},
	{"decoded-key", `return vcount(query_parse("a%20b=c"), "a b");`, 1},
	{"decoded-value", `return v0len(query_parse("k=c%2Fd"), "k");`, 3},
}

func urlQueryIRSrc(mainBody string) string {
	return "import \"core/map\";\nimport \"std/string\";\n" + urlQueryIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
