package e2eselfhost

import "testing"

// headersIRCases exercise std/headers' HeaderMap surface — case-insensitive
// get/get_all/append/set over two parallel string[] fields — through the
// self-host IR path on x86-64 + wasm (the `std/headers` row was unaudited).
// `HeaderMap` is a reserved builtin name, so the type is inlined as `Headers`,
// and `.to_lower()` as a local lookup-slice `lower`; this verifies the
// constructs the header map lowers to compile on the IR path: a struct with two
// string[] fields, functional struct-spread update (`Headers { ...h, names:
// ..., values: ... }`), `string[]` `.append`, indexed string-field compares,
// `Option[string]` `Some`/`None` with a payload-binding `match`, and chained
// struct-returning receiver methods. Each program returns a small deterministic
// int (kept <= 126). Expectations are hardcoded, verified against the native
// interp + x86-64 backends. The `append-len` case pins the `(h) len()` receiver
// method (`return h.names.len();`): it regressed #3478 (a user receiver method
// named `len` mis-dispatched to the builtin `.len()` on the struct box,
// returning a callee local's value) — fixed in irlower.fern's `.len()`
// intercept guard. FEATURE-AUDIT std/headers row.
const headersIRPrelude = `struct Headers { names: string[], values: string[] }
function lower(s: string): string {
    var alpha: string = "abcdefghijklmnopqrstuvwxyz";
    var out: string = "";
    var i: i32 = 0;
    while (i < s.len()) {
        var c: i32 = s[i] as i32;
        if (c >= 65 && c <= 90) { out = out + slice_unchecked(alpha, c - 65, c - 65 + 1); }
        else { out = out + slice_unchecked(s, i, i+1); }
        i = i + 1;
    }
    return out;
}
function header_map_new(): Headers {
    var names: string[] = [];
    var values: string[] = [];
    return Headers { names: names, values: values };
}
function (h: Headers) get(name: string): Option[string] {
    var key: string = lower(name);
    var i: i32 = 0;
    while (i < h.names.len()) {
        if (h.names[i] == key) { return Some(h.values[i]); }
        i = i + 1;
    }
    return None;
}
function (h: Headers) get_all(name: string): string[] {
    var key: string = lower(name);
    var out: string[] = [];
    var i: i32 = 0;
    while (i < h.names.len()) {
        if (h.names[i] == key) { out = out.append(h.values[i]); }
        i = i + 1;
    }
    return out;
}
function (h: Headers) append(name: string, value: string): Headers {
    return Headers { ...h, names: h.names.append(lower(name)), values: h.values.append(value) };
}
function (h: Headers) set(name: string, value: string): Headers {
    var key: string = lower(name);
    var new_names: string[] = [];
    var new_values: string[] = [];
    var inserted: boolean = false;
    var i: i32 = 0;
    while (i < h.names.len()) {
        if (h.names[i] == key) {
            if (!inserted) { new_names = new_names.append(key); new_values = new_values.append(value); inserted = true; }
        } else {
            new_names = new_names.append(h.names[i]);
            new_values = new_values.append(h.values[i]);
        }
        i = i + 1;
    }
    if (!inserted) { new_names = new_names.append(key); new_values = new_values.append(value); }
    return Headers { ...h, names: new_names, values: new_values };
}
function (h: Headers) len(): i32 { return h.names.len(); }
function get_len(h: Headers, name: string): i32 {
    match (h.get(name)) {
        Some(v) => { return v.len(); },
        None => { return 99; },
    }
    return 99;
}
`

var headersIRCases = []struct {
	name string
	main string
	want int
}{
	// the `(h) len()` receiver method counts entries: two appends -> 2. This is
	// the #3478 regression guard — a user method named `len` must shadow the
	// builtin `.len()` on the struct receiver (pre-fix: x86-64 -> 26, wasm -> 0).
	{"append-len", `var h: Headers = header_map_new(); h = h.append("Set-Cookie", "a"); h = h.append("Set-Cookie", "b"); return h.len();`, 2},
	// get is case-insensitive: "content-TYPE" finds "Content-Type" -> "text" (len 4).
	{"get-ci", `var h: Headers = header_map_new(); h = h.append("Content-Type", "text"); return get_len(h, "content-TYPE");`, 4},
	// set replaces in place: get returns the new value "22" (len 2).
	{"set-replace", `var h: Headers = header_map_new(); h = h.append("X", "1"); h = h.set("x", "22"); return get_len(h, "X");`, 2},
	// get_all collects duplicates regardless of case: 2 entries.
	{"get-all-dups", `var h: Headers = header_map_new(); h = h.append("a", "1"); h = h.append("A", "2"); return h.get_all("a").len();`, 2},
	// a missing name renders the None arm: 99.
	{"missing", `var h: Headers = header_map_new(); h = h.append("a", "1"); return get_len(h, "missing");`, 99},
}

func headersIRSrc(mainBody string) string {
	return headersIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostHeadersIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostHeadersIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range headersIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, headersIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
