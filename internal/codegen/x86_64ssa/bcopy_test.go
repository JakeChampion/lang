package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The copy and the fill loop below 64 bytes and only then reach the rep
// string instruction: a rep costs tens of cycles to start whatever the
// length, and nearly every copy this backend makes is a few bytes.
func TestSmallCopiesAndFillsDoNotUseRep(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	s := wideCallOp(f, e, "__str_concat", constStr(f, e, "ab"), constStr(f, e, "cd"))
	f.SetRet(e, wideCallOp(f, e, "__alloc_u8", constOp(f, e, 24)))
	_ = s
	asm, err := EmitAsm(f, 8)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	for _, c := range []struct{ sym, rep string }{{bcopySym, "rep movsb"}, {bfillSym, "rep stosb"}} {
		body := asm[strings.Index(asm, "\n"+c.sym+":"):]
		if i := strings.Index(body[1:], "\n\n"); i >= 0 {
			body = body[:i+1]
		}
		check := strings.Index(body, "cmp r")
		rep := strings.Index(body, c.rep)
		if check < 0 || rep < 0 || check > rep {
			t.Errorf("%s does not test the length before its rep:\n%s", c.sym, body)
		}
		if !strings.Contains(body, ", 64\n") {
			t.Errorf("%s does not switch to rep at 64 bytes:\n%s", c.sym, body)
		}
	}
}
