package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A `defer` whose action is a range `for` with no block around it. The range
// desugar turns the loop into three statements, and the defer kept a desugared
// action only when it was one, so the loop reached the lowering as a call to
// `__range`, which nothing defines. The native parser accepts only an
// expression or a block after `defer`, so this shape is the self-host
// compiler's alone and has no conformance case (#11602).
const rangeDeferActionProg = `function main(): i32 {
  let s: i32 = 0;
  let r: i32 = 0;
  loop {
    defer for i in 0..4 { s = s + i; }
    r = 30;
    break;
  }
  return r + s;
}
`

func TestSelfHostRangeForAsDeferAction(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "defer_range.fern")
	if err := os.WriteFile(src, []byte(rangeDeferActionProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 36 {
			t.Errorf("%s: exit %d, want 36", tg.target, code)
		}
	}
}
