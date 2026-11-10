package checker

import (
	"fmt"
	"strings"
	"testing"
)

func TestResultBorrowPropagationAcrossRecursiveCalls(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, borrowed := range []bool{false, true} {
			name := fmt.Sprintf("reverse=%v/borrowed=%v", reverse, borrowed)
			t.Run(name, func(t *testing.T) {
				var src strings.Builder
				src.WriteString(genericCallbackPrefix)
				for i := 0; i < 64; i++ {
					n := i
					if reverse {
						n = 63 - i
					}
					fmt.Fprintf(&src, "function step%d(h: Handle, depth: i32): Handle {", n)
					if n == 63 {
						if borrowed {
							src.WriteString("if (depth == 0) { return h; }")
						} else {
							src.WriteString("if (depth == 0) { return Handle { fd: h.fd }; }")
						}
					}
					fmt.Fprintf(&src, "return step%d(h, depth - 1); }\n", (n+1)%64)
				}
				src.WriteString(`
function through_capture(h: Handle): Handle { return call((): Handle => step0(h, 63)); }
function consume(h: Handle): i32 { return keep(through_capture(h)); }
function main(): i32 { return 0; }
`)
				if borrowed {
					wantE051(t, name, src.String())
				} else {
					wantOK(t, name, src.String())
				}
			})
		}
	}
}
