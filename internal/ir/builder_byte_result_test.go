package ir

import (
	"fmt"
	"testing"
)

func TestBuilderByteResultsReleaseAfterBorrowAndDiscard(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		t.Run(fmt.Sprint(ptrW), func(t *testing.T) {
			p := lowerImportsWith(t, `function length(bytes: u8[]): i32 { return bytes.len(); }
function main(): i32 {
  var b = buf_new(1);
  buf_push_byte(b, 255);
  var n = length(buf_take_bytes(b));
  buf_take_bytes(b);
  buf_free(b);
  return n;
}`, ptrW)
			if got := countCallPrefix(p, "main", "__fern_arr_dec"); got != 2 {
				t.Fatalf("want both owned results released, got %d array drops:\n%s", got, p)
			}
		})
	}
}
