package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// read_dir's second pass re-reads the directory, which can have changed:
// an entry added since the first pass must not be written past the
// container the first pass sized, and the length reflects what the second
// pass found.
func TestReadDirSecondPassStaysInsideTheContainer(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	f.SetRet(e, wideCallOp(f, e, "read_dir", constStr(f, e, ".")))
	asm, err := arm64ssa.EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := asm[strings.Index(asm, "\nfn_read_dir:"):]
	if end := strings.Index(body[1:], "\nfn_"); end >= 0 {
		body = body[:end+1]
	}
	if !strings.Contains(body, "cmp x24, x23\n\tb.hs ") {
		t.Errorf("the second pass does not check the fill index against the count:\n%s", body)
	}
	if !strings.Contains(body, "stur w24, [x27, #-4]") {
		t.Errorf("the length is not rewritten from the fill index after the second pass:\n%s", body)
	}
}
