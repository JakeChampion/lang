package e2ecompiler

import (
	"regexp"
	"strings"
	"testing"
)

var condBranchToAbort = regexp.MustCompile(`b\.[a-z]{2}\s+__fern_oob_abort`)

var anyBranchToAbort = regexp.MustCompile(`\b(b|bl|b\.[a-z]{2})\s+__fern_oob_abort`)

var oobBranchRangeCases = []struct {
	name       string
	src        string
	body       string
	wantBranch bool
	want       int
	runtime    bool
}{
	{"arr-get", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let i: i32 = 2; return xs[i]; }", "main", false, 3, false},
	{"arr-set", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let i: i32 = 1; xs = xs.with(i, 9); return xs[1]; }", "main", true, 9, false},
	{"runtime-arr-get", `@noinline function probe(i: i32): i32 {
    let xs: i32[] = [1, 2, 3];
    return xs[i];
}
function main(): i32 { return probe(args().len() - 2); }`, "probe", true, 0, true},
	{"runtime-arr-set", `@noinline function probe(i: i32): i32 {
    let xs: i32[] = [1, 2, 3];
    xs = xs.with(i, 9);
    return xs[1];
}
function main(): i32 { return probe(args().len() - 2); }`, "probe", true, 0, true},
}

func TestSelfHostOOBBranchRangeArm64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range oobBranchRangeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "arm64-linux", tc.src)
			body := selfHostFnBody(t, []byte(asm), tc.body)
			if got := anyBranchToAbort.MatchString(body); got != tc.wantBranch {
				t.Errorf("abort branch present=%v, want %v in %s\n%s", got, tc.wantBranch, tc.body, body)
			}
			if !tc.wantBranch && !strings.Contains(body, ".K0") {
				t.Errorf("constant read lost static array control\n%s", body)
			}
			if m := condBranchToAbort.FindAllString(asm, -1); len(m) > 0 {
				t.Errorf("conditional branch to __fern_oob_abort (R_AARCH64_CONDBR19, ±1 MiB — the link overflows once the runtime is far away): %v", m)
			}
			t.Run("run", func(t *testing.T) {
				gcc, qemu := arm64Tooling(t)
				bin := buildBinArm64(t, gcc, t.TempDir(), "prog", asm)
				if !tc.runtime {
					cmd := runArm64Bin(qemu, bin)
					out, _ := cmd.CombinedOutput()
					if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.want {
						t.Fatalf("want exit %d, state=%v\n%s", tc.want, cmd.ProcessState, out)
					}
					return
				}
				for count, name := range []string{"negative", "first", "middle", "last", "past-end"} {
					t.Run(name, func(t *testing.T) {
						args := make([]string, count)
						for i := range args {
							args[i] = "arg"
						}
						want := count
						if tc.name == "runtime-arr-set" {
							want = 2
							if count == 2 {
								want = 9
							}
						}
						if count == 0 || count == 4 {
							want = 134
						}
						cmd := runArm64Bin(qemu, bin, args...)
						out, _ := cmd.CombinedOutput()
						if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != want {
							t.Errorf("want exit %d, state=%v\n%s", want, cmd.ProcessState, out)
						}
						if want == 134 && !strings.Contains(string(out), "fern: array index out of range") {
							t.Errorf("missing array bounds diagnostic\n%s", out)
						}
					})
				}
			})
		})
	}
}
