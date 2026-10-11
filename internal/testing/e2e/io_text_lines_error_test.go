package e2e

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/oracle/interp"
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
			failure := &byteLineReadFailure{}
			i := interpLoaded(t, source)
			i.Stdin = io.MultiReader(bytes.NewReader(bytes.Repeat([]byte{tc.value}, tc.length)), failure)
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
