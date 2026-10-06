package e2e

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestIOTextLinesPartialReadError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  byte
		length int
		chunk  bool
	}{{"ascii", 'a', 17, false}, {"invalid UTF-8", 255, 17, false}, {"long binary tail", 128, 8193, false}, {"incomplete chunk", 0xe2, 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			expected := tc.length
			if tc.value >= 128 {
				expected = 0
			}
			source := e2eharness.IOTextLineReadErrorProgram(false, 7, expected, int(tc.value))
			if tc.chunk {
				source = strings.Replace(source, "lr.next_line()", "lr.next_chunk()", 1)
			}
			prog, _, err := modload.LoadSource(source)
			if err != nil {
				t.Fatal(err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatal(err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatal(err)
			}
			failure := &byteLineReadFailure{}
			i := interp.New()
			i.SetDynCoercions(info.DynCoercions)
			i.Stdin = io.MultiReader(bytes.NewReader(bytes.Repeat([]byte{tc.value}, tc.length)), failure)
			for _, ed := range prog.Enums {
				i.RegisterEnum(ed)
			}
			for _, fn := range prog.Funcs {
				i.Register(fn)
			}
			got, err := i.CallByName("main", nil)
			if err != nil || got != interp.Number(0) {
				t.Fatalf("main = %v, %v; want validated partial text and sticky I/O error", got, err)
			}
			if failure.calls != 1 {
				t.Fatalf("erroring host reader called %d times; want 1", failure.calls)
			}
		})
	}
}
