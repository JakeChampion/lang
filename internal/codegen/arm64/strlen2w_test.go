package arm64

import (
	"strings"
	"testing"
)

// TestStrLen2WPreservesLenRegister pins that emitStrLen2W leaves its len
// register intact on both arms. __fern_strcat, __str_slice and the path
// helpers extract the byte length and then hand the same len word to
// emitStrDataPtr2W; if the inline arm wrote the nibble back into that
// register, the flag bit would be gone and an inline string's packed bytes
// would be dereferenced as a heap pointer.
func TestStrLen2WPreservesLenRegister(t *testing.T) {
	g := &generator{stringLabel: map[string]string{}}
	g.emitStrLen2W("w23", "x20")
	g.flushPeep()
	for _, line := range strings.Split(g.out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasSuffix(f[0], ":") {
			continue
		}
		switch f[0] {
		case "tbnz", "b", "cbz", "cbnz":
			continue
		}
		dst := strings.TrimSuffix(f[1], ",")
		if dst == "x20" || dst == "w20" {
			t.Fatalf("emitStrLen2W writes its len register: %q\n%s", line, g.out.String())
		}
	}
	if !strings.Contains(g.out.String(), "ubfx x23, x20, #56, #4") {
		t.Errorf("inline arm must extract the length nibble into the destination's 64-bit alias:\n%s", g.out.String())
	}
}
