package e2ecompiler

import "testing"

const headersIRPrelude = `struct Headers { names: string[], values: string[] }
function lower(s: string): string {
    let alpha: string = "abcdefghijklmnopqrstuvwxyz";
    let out: string = "";
    let i: i32 = 0;
    while (i < s.len()) {
        let c: i32 = s[i] as i32;
        if (c >= 65 && c <= 90) { out = out + slice_unchecked(alpha, c - 65, c - 65 + 1); }
        else { out = out + slice_unchecked(s, i, i+1); }
        i = i + 1;
    }
    return out;
}
function header_map_new(): Headers {
    let names: string[] = [];
    let values: string[] = [];
    return Headers { names: names, values: values };
}
function (h: Headers) get(name: string): Option[string] {
    let key: string = lower(name);
    let i: i32 = 0;
    while (i < h.names.len()) {
        if (h.names[i] == key) { return Some(h.values[i]); }
        i = i + 1;
    }
    return None;
}
function (h: Headers) get_all(name: string): string[] {
    let key: string = lower(name);
    let out: string[] = [];
    let i: i32 = 0;
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
    let key: string = lower(name);
    let new_names: string[] = [];
    let new_values: string[] = [];
    let inserted: boolean = false;
    let i: i32 = 0;
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
	{"append-len", `let h: Headers = header_map_new(); h = h.append("Set-Cookie", "a"); h = h.append("Set-Cookie", "b"); return h.len();`, 2},
	{"get-ci", `let h: Headers = header_map_new(); h = h.append("Content-Type", "text"); return get_len(h, "content-TYPE");`, 4},
	{"set-replace", `let h: Headers = header_map_new(); h = h.append("X", "1"); h = h.set("x", "22"); return get_len(h, "X");`, 2},
	{"get-all-dups", `let h: Headers = header_map_new(); h = h.append("a", "1"); h = h.append("A", "2"); return h.get_all("a").len();`, 2},
	{"missing", `let h: Headers = header_map_new(); h = h.append("a", "1"); return get_len(h, "missing");`, 99},
}

func headersIRSrc(mainBody string) string {
	return headersIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
