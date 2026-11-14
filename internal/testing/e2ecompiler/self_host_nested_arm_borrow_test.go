package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const nestedArmBorrowDecls = `struct St { n: i32, files: Reader[] }
struct Got { st: St, v: i32 }
struct Info { st: St, kind: string, size: i64 }
@noinline
function parse(own s: St, k: i32): Got { return Got { st: St { ...s, n: s.n + k }, v: k }; }
@noinline
function pick(k: i32): Result[(string, i64), string] { return if (k > 2) { Ok(("a", 3)) } else { Err("no") }; }
@noinline
function shut(own r: Reader): i32 { r.close(); return 1; }
`

// A function whose result is a value match with a nested pattern in an arm.
// The result borrows a handle only when an arm gives a borrowed handle: the
// form of the pattern does not decide it.
func nestedArmBorrowCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const useInfo = `let got = info(s, i); s = got.st;`
	const useHandle = `let h = same(f, i); s = St { n: s.n + shut(h), files: s.files };`
	const useFirst = `let h = first(s, i); s = St { n: s.n + shut(h), files: s.files };`
	cases := []struct {
		name, fn, use string
		reject        bool
	}{
		{"struct-with-the-record/nested", `function info(s: St, k: i32): Info { return match (pick(k)) { Ok((kind, size)) => Info { st: s, kind: kind, size: size }, Err(why) => Info { st: s, kind: "", size: 0 } }; }`, useInfo, false},
		{"struct-with-the-record/plain", `function info(s: St, k: i32): Info { return match (pick(k)) { Ok(pair) => Info { st: s, kind: pair.0, size: pair.1 }, Err(why) => Info { st: s, kind: "", size: 0 } }; }`, useInfo, false},
		{"the-handle-of-a-parameter/nested", `function same(r: Reader, k: i32): Reader { return match (pick(k)) { Ok((kind, size)) => r, Err(why) => r }; }`, useHandle, true},
		{"the-handle-of-a-parameter/plain", `function same(r: Reader, k: i32): Reader { return match (pick(k)) { Ok(pair) => r, Err(why) => r }; }`, useHandle, true},
		{"a-handle-in-a-parameter/nested", `function first(s: St, k: i32): Reader { return match (pick(k)) { Ok((kind, size)) => s.files[0], Err(why) => s.files[0] }; }`, useFirst, true},
		{"a-handle-in-a-parameter/plain", `function first(s: St, k: i32): Reader { return match (pick(k)) { Ok(pair) => s.files[0], Err(why) => s.files[0] }; }`, useFirst, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := nestedArmBorrowDecls + "@noinline\n" + tc.fn + `
function main(): i32 {
  let s = St { n: 0, files: [] };
  let f = stdin();
  for i in 0..10 {
    let named = parse(s, i);
    s = named.st;
    ` + tc.use + `
  }
  return s.n;
}
`
			check := func(label string, ds []driverDiag) {
				t.Helper()
				refused := false
				for _, d := range ds {
					if d.code == "E051" {
						refused = true
					} else {
						t.Fatalf("%s: a diagnostic that is not E051: %v", label, ds)
					}
				}
				if refused != tc.reject {
					t.Fatalf("%s: E051 reported is %v, want %v: %v", label, refused, tc.reject, ds)
				}
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostNestedArmBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "nested_arm_borrow_checker")
	nestedArmBorrowCases(t, bin, runner, dir)
}

func TestSelfHostNestedArmBorrowWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "nested_arm_borrow_checker.wasm", e2eharness.TargetWasm32Wasi)
	nestedArmBorrowCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}
