package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const projectedViewPhiProgram = `
struct Pair { left: u8[], right: u8[] }
struct Nested { pair: Pair }
enum Payload { Bytes(Pair), Empty }

function copy(input: [u8]): u8[] {
  let out: u8[] = [];
  for byte in input { out = out.append(byte); }
  return out;
}

@noinline function record(pair: Pair, left: boolean): u8[] {
  let view = if (left) { pair.left[:] } else { pair.right[:] };
  return copy(view[1:]);
}

@noinline function tuple(pair: (u8[], u8[]), left: boolean): u8[] {
  let view = if (left) { pair.0[:] } else { pair.1[:] };
  return copy(view[1:]);
}

@noinline function array(pair: u8[][], left: boolean): u8[] {
  let view = if (left) { pair[0][:] } else { pair[1][:] };
  return copy(view[1:]);
}

@noinline function nested(value: Nested, left: boolean): u8[] {
  let view = if (left) { value.pair.left[:] } else { value.pair.right[:] };
  return copy(view[1:]);
}

@noinline function payload(value: Payload, left: boolean): u8[] {
  let Payload.Bytes(pair) = value else { return []; };
  let view = if (left) { pair.left[:] } else { pair.right[:] };
  return copy(view[1:]);
}

@noinline function reader(pair: Pair): (boolean) => u8[] {
  return (left: boolean): u8[] => {
    let view = if (left) { pair.left[:] } else { pair.right[:] };
    return copy(view[1:]);
  };
}

@noinline function returned_view(pair: Pair, left: boolean): [u8] {
  return if (left) { pair.left[1:] } else { pair.right[1:] };
}

function check(values: [u8], left: boolean): boolean {
  return values.len() == 2 && values[0] == (if (left) { 13u8 } else { 23u8 }) && values[1] == (if (left) { 17u8 } else { 29u8 });
}

function main(): i32 {
  for _ in 0..100 {
    let pair = Pair { left: [11u8, 13u8, 17u8], right: [19u8, 23u8, 29u8] };
    let read = reader(pair);
    for left in [true, false, true, false] {
      if (!check(record(pair, left), left)) { return 1; }
      if (!check(tuple((pair.left, pair.right), left), left)) { return 2; }
      if (!check(array([pair.left, pair.right], left), left)) { return 3; }
      if (!check(nested(Nested { pair: pair }, left), left)) { return 4; }
      if (!check(payload(Payload.Bytes(pair), left), left)) { return 5; }
      if (!check(read(left), left)) { return 6; }
      if (!check(returned_view(pair, left), left)) { return 7; }
    }
  }
  print("projected view joins passed");
  return 0;
}
`

func TestSelfHostProjectedViewPhi(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, projectedViewPhiProgram, []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "projected view joins passed") {
				t.Fatalf("projected view joins: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
}
