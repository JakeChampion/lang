package e2e

import "testing"

// A local built from a literal and grown in place is as fresh as a call
// result, so it may be handed to an `own` parameter at its last use in any
// position (#10864): one through four, and nested's at any depth, would be E051
// without that. Five is
// the call-bound string the move already took, which nulled its two-word
// slot with one word on arm64 and wasm and passed the callee half a string
// (#10992). Every move must free each value exactly once.
const ownLiteralLocalSrc = `
struct Box { xs: i64[], tag: string }
@noinline function take(own xs: i64[]): i32 { return xs.len() as i32; }
@noinline function takeb(own b: Box): i32 { return b.xs.len() as i32 + b.tag.len() as i32; }
@noinline function takes(own s: string): i32 { return s.len() as i32; }
@noinline function mk(): string { return "ab" + "cd"; }
function one(): i32 {
  let xs: i64[] = [];
  xs = xs.append(1);
  return take(xs) + 0;
}
function two(): i32 {
  let xs: i64[] = [5, 6];
  xs = xs.append(7);
  let r: i32 = take(xs);
  return r;
}
function three(): i32 {
  let b: Box = Box { xs: [1, 2], tag: "t" };
  b = Box { ...b, tag: b.tag + "u" };
  let r: i32 = takeb(b);
  return r;
}
function four(): i32 {
  let s: string = "ab";
  s = s + "cd";
  let r: i32 = takes(s);
  return r;
}
function five(): i32 {
  let s: string = mk();
  return takes(s);
}
function main(): i32 {
  function nested(): i32 {
    let b: Box = Box { xs: [], tag: "nest" };
    let r: i32 = takeb(b);
    return r + 1;
  }
  return one() + two() + three() + four() + five() + nested();
}
`

func TestOwnLiteralLocalX86_64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, ownLiteralLocalSrc, 21, runSanitizeX86_64)
}

func TestOwnLiteralLocalArm64Sanitized(t *testing.T) {
	checkSanitizedBalanced(t, ownLiteralLocalSrc, 21, runSanitizeArm64)
}

func TestOwnLiteralLocalWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, ownLiteralLocalSrc); got != 21 {
		t.Fatalf("wasm exited %d, want 21", got)
	}
}
