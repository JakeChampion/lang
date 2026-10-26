package e2ecompiler

import (
	"bytes"
	"os/exec"
	"reflect"
	"testing"
)

func TestSelfHostCompoundArraySlots(t *testing.T) {
	bin, runner, _ := buildCheckerCodesBin(t)
	const union = `struct A { a: i32 } struct B { b: i32 } struct P { x: i32 } type U = A | B; `
	const trait = `trait T { function size(self: Self): i32; } struct P { x: i32 } `
	for _, tc := range []struct {
		name, source string
		codes        []string
	}{
		{"nested-string", `function f(): str[][] { return [[5]]; }`, []string{"E034"}},
		{"deep-string", `function f(): str[][][] { return [[[5]]]; }`, []string{"E034"}},
		{"array-variable", `function f(xs: i32[]): str[][] { return [xs]; }`, []string{"E002"}},
		{"nested-view-literal", `function take(xs: [str][]): i32 { return xs.len(); } function f(s: str): i32 { return take([[s]]); }`, []string{"E038"}},
		{"nested-union", union + `function f(a: A): U[][] { return [[a]]; }`, nil},
		{"option-union", union + `function f(a: A): Option[U[]] { return Some([a]); }`, nil},
		{"const-union", union + `const VALUES: U[] = [A { a: 1 }];`, nil},
		{"const-nonmember", union + `const VALUES: U[] = [P { x: 1 }];`, []string{"E034"}},
		{"const-array-type", union + `const VALUES: U[] = [A { a: 1 }]; function take(xs: A[]): i32 { return xs.len(); } function f(): i32 { return take(VALUES); }`, []string{"E038"}},
		{"nested-nonmember", union + `function f(p: P): U[][] { return [[p]]; }`, []string{"E034"}},
		{"value-if-trait", trait + `function f(p: P, c: boolean): i32 { let xs: dyn T[] = if (c) { [p] } else { [] }; return xs.len(); }`, []string{"E034"}},
		{"value-if-local", trait + `function f(c: boolean): i32 { let xs: dyn T[] = if (c) { let p = P { x: 1 }; [p] } else { [] }; return xs.len(); }`, []string{"E034"}},
		{"value-if-valid", trait + `impl T for P { function size(self: Self): i32 { return self.x; } } function f(p: P, c: boolean): i32 { let xs: dyn T[] = if (c) { [p] } else { [] }; return xs.len(); }`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.source + ` function main(): i32 { return 0; }`
			if got := goCheckerDiagCodes(t, t.TempDir(), source); !reflect.DeepEqual(got, tc.codes) {
				t.Errorf("Go codes: got %v, want %v", got, tc.codes)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = bytes.NewBufferString(source)
			out := runCheckerDriver(t, cmd, tc.name)
			var codes []string
			for _, d := range driverDiags(out) {
				codes = append(codes, d.code)
			}
			if !reflect.DeepEqual(codes, tc.codes) {
				t.Errorf("self-host codes: got %v, want %v\n%s", codes, tc.codes, out)
			}
		})
	}
}

func TestSelfHostConstUnionArray(t *testing.T) {
	const source = `struct A { a: i32 }
struct B { b: i32 }
type U = A | B;
const VALUES: U[] = [A { a: 3 }, B { b: 4 }];
function sum(xs: U[]): i32 {
  let total: i32 = 0;
  for value in xs {
    match (value) {
      A(value) => { total = total + value.a; },
      B(value) => { total = total + value.b; }
    }
  }
  return total;
}
function main(): i32 { return sum(VALUES) + sum(VALUES) - 14; }
`
	if got := interpExit(t, buildLangBinForInterp(t), source); got != 0 {
		t.Fatalf("interpreter exit %d, want zero", got)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, source, target, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want zero\n%s", code, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil {
				t.Fatal(err)
			}
			if allocs != frees || live != 0 {
				t.Fatalf("unbalanced census: %s", stderr)
			}
		})
	}
}
