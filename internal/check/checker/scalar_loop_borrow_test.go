package checker

import "testing"

func TestScalarLoopCopiesKeepFreshArrayResults(t *testing.T) {
	for _, iter := range []string{"input", "input[:2]"} {
		wantOK(t, iter, `
function append_bytes(own output: u8[], input: [u8]): u8[] {
  for byte in `+iter+` { output = output.append(byte); }
  return output;
}
function main(): i32 {
  let input: u8[] = [1, 2, 3];
  let result = append_bytes(append_bytes([], input[:2]), [4u8, 5u8]);
  return result.len() + input.len();
}`)
	}
}

func TestHandleLoopElementsRemainBorrowed(t *testing.T) {
	wantE051(t, "handle-bearing loop append", `
struct Held { reader: Reader }
function append_rows(own output: Held[], input: [Held]): Held[] {
  for row in input { output = output.append(row); }
  return output;
}
function main(): i32 {
  let input = [Held { reader: stdin() }];
  let result = append_rows(append_rows([], input[:]), []);
  return result.len() + input.len();
}`)
}
