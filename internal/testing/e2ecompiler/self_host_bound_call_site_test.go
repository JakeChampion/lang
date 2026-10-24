package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var boundCallSiteCases = []struct {
	name     string
	src      string
	wantE021 []string
}{
	{"imported-trait-struct-literal",
		"import \"core/cmp\";\nstruct Pt { x: i32 }\nfunction need[T: cmp.Eq](a: T): i32 { return 1; }\nfunction main(): i32 { return need(Pt { x: 1 }); }\n",
		[]string{"4:35 type argument T = Pt does not implement trait cmp.Eq required by need"}},
	{"imported-trait-local-and-call-argument",
		"import \"core/cmp\";\nstruct Pt { x: i32 }\nfunction mk(): Pt { return Pt { x: 1 }; }\nfunction need[T: cmp.Eq](a: T): i32 { return 1; }\nfunction main(): i32 { let p = Pt { x: 2 }; return need(p) + need(mk()); }\n",
		[]string{
			"5:56 type argument T = Pt does not implement trait cmp.Eq required by need",
			"5:66 type argument T = Pt does not implement trait cmp.Eq required by need",
		}},
	{"aliased-import-trait",
		"import \"core/cmp\" as c;\nstruct Pt { x: i32 }\nfunction need[T: c.Eq](a: T): i32 { return 1; }\nfunction main(): i32 { return need(Pt { x: 1 }); }\n",
		[]string{"4:35 type argument T = Pt does not implement trait cmp.Eq required by need"}},
	{"imported-trait-impl-ok",
		"import \"core/cmp\";\nstruct Pt { x: i32 }\nimpl cmp.Eq for Pt { function eq(self: Pt, other: Pt): boolean { return self.x == other.x; } }\nfunction need[T: cmp.Eq](a: T): i32 { return 1; }\nfunction main(): i32 { return need(Pt { x: 1 }); }\n",
		nil},
	{"imported-trait-derive-ok",
		"import \"core/cmp\";\n@derive(cmp.Eq)\nstruct Pt { x: i32 }\nfunction need[T: cmp.Eq](a: T): i32 { return 1; }\nfunction main(): i32 { return need(Pt { x: 1 }); }\n",
		nil},
	{"one-of-two-bounds-missing",
		"import \"core/cmp\";\n@derive(cmp.Eq)\nstruct Pt { x: i32 }\nfunction need[T: cmp.Eq + cmp.Hash](a: T): i32 { return 1; }\nfunction main(): i32 { return need(Pt { x: 1 }); }\n",
		[]string{"5:35 type argument T = Pt does not implement trait cmp.Hash required by need"}},
	{"own-trait-primitive",
		"trait Tr { function f(self: Self): i32; }\nfunction need[T: Tr](a: T): i32 { return 1; }\nfunction main(): i32 { return need(3); }\n",
		[]string{"3:35 type argument T = i32 does not implement trait Tr required by need"}},
	{"stdlib-free-generic-struct",
		"import \"core/cmp\";\nstruct Pt { x: i32 }\nfunction main(): i32 { let ps: Pt[] = [Pt { x: 1 }]; if (cmp.contains(ps, Pt { x: 1 })) { return 1; } return 0; }\n",
		[]string{"3:70 type argument T = Pt does not implement trait cmp.Eq required by cmp.contains"}},
	{"stdlib-method-generic-struct",
		"import \"std/array\";\nimport \"std/option\";\nstruct Pt { x: i32 }\nfunction main(): i32 { let ps: Pt[] = [Pt { x: 1 }]; return ps.index_of(Pt { x: 1 }).unwrap_or(0); }\n",
		[]string{"4:72 type argument T = Pt does not implement trait cmp.Eq required by Array.index_of"}},
	{"stdlib-method-generic-char-ok",
		"import \"std/array\";\nfunction main(): i32 { let cs: char[] = ['a', 'b']; if (cs.contains('a')) { return 1; } return 0; }\n",
		nil},
	{"stdlib-method-generic-primitives-ok",
		"import \"std/array\";\nfunction main(): i32 { let xs: i32[] = [1, 2]; let bs: u8[] = [3]; let ss: string[] = [\"a\"]; let us: usize[] = []; if (xs.contains(2) && bs.contains(3) && ss.contains(\"a\") && !us.contains(1)) { return 0; } return 1; }\n",
		nil},
	{"impl-for-str-ok",
		"trait Shout { function shout(self: Self): string; }\nimpl Shout for str { function shout(self: Self): string { return self + \"!\"; } }\nfunction loud[T: Shout](x: T): string { return x.shout(); }\nfunction main(): i32 { let o: string = \"ab\" + \"cd\"; let s: str = slice_unchecked(o, 1, 3); return loud(s).len() + loud(o).len(); }\n",
		nil},
	{"impl-for-string-ok-at-str",
		"trait Shout { function shout(self: Self): string; }\nimpl Shout for string { function shout(self: Self): string { return self + \"!\"; } }\nfunction loud[T: Shout](x: T): string { return x.shout(); }\nfunction main(): i32 { let o: string = \"ab\" + \"cd\"; let s: str = slice_unchecked(o, 1, 3); return loud(s).len() + loud(o).len(); }\n",
		nil},
	{"generic-into-generic-ok",
		"import \"core/cmp\";\nfunction need[T: cmp.Eq](a: T): i32 { return 1; }\nfunction outer[U](x: U): i32 { return need(x); }\nfunction main(): i32 { return 0; }\n",
		nil},
	{"unknown-trait-in-bound",
		"function need[T: Bogus](a: T): i32 { return 1; }\nfunction need2[T: zz.Bogus](a: T): i32 { return 1; }\nfunction main(): i32 { return need(3) + need2(4); }\n",
		[]string{
			"1:1 unknown trait \"Bogus\" in bound on type parameter T",
			"2:1 unknown trait \"zz.Bogus\" in bound on type parameter T",
			"3:35 type argument T = i32 does not implement trait Bogus required by need",
			"3:46 type argument T = i32 does not implement trait zz.Bogus required by need2",
		}},
	{"bound-trait-arity",
		"trait Conv[U] { function conv(self: Self): U; }\nstruct P { x: i32 }\nimpl Conv[i32] for P { function conv(self: P): i32 { return self.x; } }\nfunction f[T: Conv](x: T): i32 { return 1; }\nfunction main(): i32 { return f(P { x: 3 }); }\n",
		[]string{"4:1 trait Conv takes 1 type argument(s), 0 supplied in bound on type parameter T"}},
	{"generic-fn-value",
		"import \"core/cmp\";\nstruct Pt { x: i32 }\nfunction need[T: cmp.Eq](a: T): i32 { return 1; }\nfunction main(): i32 { let f: (Pt) => i32 = need; return f(Pt { x: 1 }); }\n",
		[]string{"4:45 type argument T = Pt does not implement trait cmp.Eq required by need"}},
}

var checkDiagRE = regexp.MustCompile(`^[^:]*main\.fern:(\d+):(\d+): error\[(E\d{3})\]: (.*)$`)

func checkDiags(t *testing.T, cmd *exec.Cmd) []string {
	t.Helper()
	out, _ := cmd.CombinedOutput()
	var ds []string
	for _, line := range strings.Split(string(out), "\n") {
		if m := checkDiagRE.FindStringSubmatch(line); m != nil {
			ds = append(ds, m[1]+":"+m[2]+" "+m[3]+" "+m[4])
		}
	}
	sort.Strings(ds)
	return ds
}

func TestSelfHostBoundCallSiteAgreesWithGo(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	for _, tc := range boundCallSiteCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			goDiags := checkDiags(t, exec.Command(oracle, "-check", p))
			shDiags := checkDiags(t, exec.Command(cli, "-check", p, root))
			if strings.Join(goDiags, "\n") != strings.Join(shDiags, "\n") {
				t.Errorf("diagnostics differ\n--- Go ---\n%s\n--- self-host ---\n%s", strings.Join(goDiags, "\n"), strings.Join(shDiags, "\n"))
			}
			var want []string
			for _, w := range tc.wantE021 {
				pos, msg, _ := strings.Cut(w, " ")
				want = append(want, pos+" E021 "+msg)
			}
			sort.Strings(want)
			if strings.Join(want, "\n") != strings.Join(goDiags, "\n") {
				t.Errorf("Go checker diagnostics\n%s\nwant\n%s", strings.Join(goDiags, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

const usizeEqOrdHashSrc = `import "core/cmp";
import "std/array";
import "std/option";
function main(): i32 {
  let us: usize[] = [3, 7, 7];
  let seven: usize = 7;
  if (!us.contains(seven)) { return 1; }
  if (us.index_of(seven).unwrap_or(9) != 1) { return 2; }
  if (cmp.max(us[0], seven) != seven) { return 3; }
  if (seven.hash() != seven.hash()) { return 4; }
  if (us[0].cmp(seven) != 0 - 1 || seven.cmp(us[0]) != 1 || seven.cmp(us[1]) != 0) { return 5; }
  return 0;
}
`

func TestSelfHostUsizeEqOrdHashRuns(t *testing.T) {
	if _, code := e2eharness.RunInterp(t, usizeEqOrdHashSrc); code != 0 {
		t.Errorf("interpreter: exit %d, want 0", code)
	}
	cli := e2eharness.SelfHostCLI(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(usizeEqOrdHashSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	build := exec.Command(cli, "-target", e2eharness.HostSelfHostTarget(), "-o", bin, in, e2eharness.SelfHostStdlibRoot(t))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("compiled: exit %d, want 0", code)
	}
}
