package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// count passes a buffer handle read out of its parameter to buf_len. Spliced
// into main, it would let main release w, and run Blk's drop, before buf_len
// reads the buffer. The drop pushes a marker byte, so the count would be 2.
const inlineReadWordProgram = `import "core/mem";
struct Blk { h: usize }
impl mem.Drop for Blk {
  function drop(self: Blk): void { buf_push_byte(self.h, 9); }
}
struct W { blk: Blk, tag: i32 }
function (w: W) count(): i32 { return buf_len(w.blk.h); }
@noinline function make(h: usize): W { return W { blk: Blk { h: h }, tag: 1 }; }
function main(): i32 {
  let h: usize = buf_new(8);
  buf_push_byte(h, 1);
  let w: W = make(h);
  let n: i32 = w.count();
  buf_free(h);
  if (n != 1) { return n; }
  print("ok");
  return 0;
}
`

// A tiny body that passes a word read out of memory to a call stays out of
// line, so its parameter lives until the call returns.
func TestSelfHostInlineKeepsReadWordOwner(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		for _, env := range [][]string{nil, {"FERN_SANITIZE=1"}} {
			name := target
			if env != nil {
				name += "/sanitize"
			}
			t.Run(name, func(t *testing.T) {
				bin := e2eharness.CompileSelfHostSource(t, target, inlineReadWordProgram, env)
				out, err := runScaleTarget(t, target, bin).CombinedOutput()
				if err != nil || !strings.HasPrefix(string(out), "ok") {
					t.Fatalf("%v:\n%s", err, out)
				}
			})
		}
	}
}
