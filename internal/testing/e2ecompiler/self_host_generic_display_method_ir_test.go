package e2ecompiler

import "testing"

const genericDisplayMethodIRPrelude = `trait Display { function to_string(self: Self): string; }
impl Display for string { function to_string(self: Self): string { return self; } }
struct Tag { name: string }
impl Display for Tag { function to_string(self: Self): string { return self.name; } }
pub function join_d[T: Display](xs: T[], sep: string): string {
    let out: string = "";
    let i: i32 = 0;
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
	{"string-join", `let ss: string[] = ["ab", "cd", "ef"]; return join_d(ss, "-").len();`, 8},
	{"struct-join", `let ts: Tag[] = [Tag { name: "xyz" }, Tag { name: "w" }]; return join_d(ts, ",").len();`, 5},
	{"two-types", `let ss: string[] = ["ab", "cd", "ef"]; let ts: Tag[] = [Tag { name: "xyz" }, Tag { name: "w" }]; return join_d(ss, "-").len() + join_d(ts, ",").len();`, 13},
	{"empty-sep", `let ss: string[] = ["a", "b", "c"]; return join_d(ss, "").len();`, 3},
}

func genericDisplayMethodIRSrc(mainBody string) string {
	return genericDisplayMethodIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

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
