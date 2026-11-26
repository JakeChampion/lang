package e2ecompiler

import (
	"strings"
	"testing"
)

// A keep must not start before a step that retains its donor. Here the
// spreads of `done` share their field reads, and `done` goes into an array
// between the first spread and the last one.
func TestSelfHostKeepGateBeforeRetain(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, keepGateBeforeRetainSrc, target, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			if strings.Contains(stderr, "leakcheck:") && !strings.Contains(stderr, "live_bytes=0") {
				t.Fatalf("leak:\n%s", stderr)
			}
		})
	}
}

const keepGateBeforeRetainSrc = `struct SInst { kind_tag: i32, result: i32, args: i32[], imm: i32, str: string, ir_kind: i32 }
struct STerm { kind_tag: i32, cond: i32, target: i32, t: i32, f: i32, value: i32 }
struct SBlock { id: i32, insts: SInst[], term: STerm, preds: i32[] }
struct SFunc { name: string, nparams: i32, nvals: i32, blocks: SBlock[], entry: i32, takes_env: boolean }
function inst(k: i32, v: i32, args: i32[], imm: i32, name: string): SInst {
  return SInst { ir_kind: name.len(), kind_tag: k, result: v, args: args, imm: imm, str: name };
}
function call(v: i32, a: i32, name: string): SInst {
  return inst(9, v, [a], 0, name);
}
@noinline function expect(f: SFunc, modes: i32[], owned: boolean, status: i32, phrase: string, id: i32): void {
  let n = 0;
  for b in f.blocks {
    n += b.id + b.insts.len() + b.term.t + b.preds.len();
  }
  if (n != 39 || owned || phrase.len() != 1) {
    exit(id);
  }
}
function br(id: i32): STerm {
  return STerm { kind_tag: 2, value: 0, target: id, t: 0, f: 0, cond: 0 };
}
function ret(v: i32): STerm {
  return STerm { kind_tag: 1, value: v, target: 0, t: 0, f: 0, cond: 0 };
}
function main(): i32 {
  let done = SBlock { id: 37, preds: [17], insts: [call(6, 3, "dec")], term: ret(2) };
  let f = SFunc { name: "loop", nparams: 2, nvals: 7, entry: 7, takes_env: false, blocks: [done] };
  expect(SFunc { ...f, blocks: [SBlock { ...done, preds: [1] }] }, [2, 0], false, 0, "a", 53);
  expect(SFunc { ...f, blocks: [done] }, [2, 0], false, 1, "b", 54);
  expect(SFunc { ...f, blocks: [SBlock { ...done, insts: [call(6, 3, "unique")] }] }, [1, 0], false, 0, "c", 65);
  let out = SBlock { ...done, preds: [27], insts: [call(3, 0, "dec")] };
  expect(SFunc { ...f, nvals: 4, blocks: [out] }, [2, 0], false, 2, "d", 66);
  return 0;
}
`
