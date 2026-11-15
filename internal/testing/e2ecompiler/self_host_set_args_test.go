package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type setArgsCase struct {
	name, src string
	args      []string
	want      int
}

var setArgsCases = []setArgsCase{
	{"shift", `function main(): i32 {
    let argv: string[] = args();
    if (argv.len() != 3) { return 1; }
    let rest: string[] = [];
    for a in argv[1:] { rest = rest.append(a); }
    set_args(rest);
    let now: string[] = args();
    if (now.len() != 2) { return 2; }
    if (now[0] != "uname") { return 3; }
    if (now[1] != "--bogus") { return 4; }
    return 42;
}`, []string{"uname", "--bogus"}, 42},
	{"loop", `function shifted(): string[] {
    let rest: string[] = [];
    for a in args()[1:] { rest = rest.append(a + "!"); }
    return rest;
}
function main(): i32 {
    set_args(shifted());
    if (args().len() != 2 || args()[1] != "y!") { return 2; }
    set_args(shifted());
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        let a: string[] = args();
        total = total + a.len() + a[0].len();
        i = i + 1;
    }
    if (args()[0] != "y!!") { return 3; }
    if (total != 4000) { return 4; }
    return 42;
}`, []string{"x", "y"}, 42},
	{"lent-array", `function twice(s: string): string { return s + "-" + s; }
function main(): i32 {
    let mine: string[] = [twice("a"), twice("b")];
    set_args(mine);
    mine = mine.with(0, twice("z"));
    mine = mine.append(twice("c"));
    let got: string[] = args();
    if (got.len() != 2) { return 1; }
    if (got[0] != "a-a") { return 2; }
    if (mine[0] != "z-z" || mine.len() != 3) { return 3; }
    let kept: string = args()[1];
    set_args([twice("q")]);
    if (kept != "b-b") { return 4; }
    if (args().len() != 1 || args()[0] != "q-q") { return 5; }
    return 42;
}`, nil, 42},
	{"set-to-itself", `function twice(s: string): string { return s + "-" + s; }
function main(): i32 {
    set_args([twice("a"), twice("b")]);
    let i: i32 = 0;
    while (i < 10) {
        set_args(args());
        i = i + 1;
    }
    if (args().len() != 2 || args()[1] != "b-b") { return 1; }
    return 42;
}`, nil, 42},
}

func TestSelfHostSetArgs(t *testing.T) {
	cli := buildSelfHostCLI(t)
	native := buildFernCLIBin(t)
	cases := append([]setArgsCase(nil), setArgsCases...)
	for _, helper := range []struct{ name, prefix string }{
		{"lent-through-call", "@noinline function save(a: string[]): void { set_args(a); }\n"},
		{"lent-through-two-calls", "@noinline function keep(a: string[]): void { set_args(a); }\n@noinline function save(a: string[]): void { keep(a); }\n"},
	} {
		source := helper.prefix + strings.Replace(setArgsCases[2].src, "set_args(mine);", "save(mine);", 1)
		cases = append(cases, setArgsCase{name: helper.name, src: source, want: 42})
	}
	cases = append(cases, setArgsCase{name: "nonretaining-call", src: `
@noinline function read(a: string[]): i32 { return a[0].len(); }
function main(): i32 {
    let a = ["one", "two"];
    if (read(a) != 3) { return 1; }
    let copies = __arr_push_shared_count();
    a = a.append("three");
    if (a.len() != 3 || a[2] != "three") { return 2; }
    if (__arr_push_shared_count() != copies) { return 3; }
    return 42;
}`, want: 42})
	for _, tc := range cases {
		src := filepath.Join(t.TempDir(), tc.name+".fern")
		if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.name+"/interp", func(t *testing.T) {
			cmd := exec.Command(native, append([]string{"-interp", src}, tc.args...)...)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit %d, want %d\n%s", code, tc.want, out)
			}
		})
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				stderr, code := cli.exitOfFileArgs(t, src, target, nil, tc.args,
					"FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != tc.want || strings.Contains(stderr, "fern-sanitizer:") {
					t.Fatalf("exit %d, want %d without a sanitizer finding\n%s", code, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
