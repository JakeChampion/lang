package e2eselfhost

import "testing"

// genericDisplayMethodIRCases pin the fix for #3893: a generic body that
// calls a trait METHOD returning a `string` on a type-parameter element —
// `xs[i].to_string()` in a `[T: Display]` join — must monomorphise per
// element type so the call dispatches the concrete type's `.to_string()`
// (string identity, a struct field read), NOT the i32 runtime helper.
//
// The wasm backend skipped the monomorphisation x86-64 already ran, so
// `.to_string()` on a string element defaulted to `__fern_i32_to_str` and
// returned a corrupt string. These cases cover both element types (string
// identity + a `Tag` struct field) and both backends; each returns a small
// deterministic int (<= 125, wasm exit-code safe), oracle-checked against the
// interpreter.
const genericDisplayMethodIRPrelude = `trait Display { function to_string(self: Self): string; }
impl Display for string { function to_string(self: Self): string { return self; } }
struct Tag { name: string }
impl Display for Tag { function to_string(self: Self): string { return self.name; } }
pub function join_d[T: Display](xs: T[], sep: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < xs.len()) {
        if (i > 0) { out = out + sep; }
        out = out + xs[i].to_string();
        i = i + 1;
    }
    return out;
}
`

var genericDisplayMethodIRCases = []struct {
	name string
	main string
	want int
}{
	// string identity join: "ab-cd-ef" -> 8.
	{"string-join", `var ss: string[] = ["ab", "cd", "ef"]; return join_d(ss, "-").len();`, 8},
	// struct (field) join: "xyz,w" -> 5.
	{"struct-join", `var ts: Tag[] = [Tag { name: "xyz" }, Tag { name: "w" }]; return join_d(ts, ",").len();`, 5},
	// both element types -> two clones -> 8 + 5 = 13.
	{"two-types", `var ss: string[] = ["ab", "cd", "ef"]; var ts: Tag[] = [Tag { name: "xyz" }, Tag { name: "w" }]; return join_d(ss, "-").len() + join_d(ts, ",").len();`, 13},
	// empty separator (the regression that returned doubled length pre-fix).
	{"empty-sep", `var ss: string[] = ["a", "b", "c"]; return join_d(ss, "").len();`, 3},
}

func genericDisplayMethodIRSrc(mainBody string) string {
	return genericDisplayMethodIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostGenericDisplayMethodIR compiles each case with the self-host
// CLI for x86-64 and wasm and checks the exit code. The wasm cases are the
// #3893 regression guard.
func TestSelfHostGenericDisplayMethodIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range genericDisplayMethodIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, genericDisplayMethodIRSrc(tc.main), target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
