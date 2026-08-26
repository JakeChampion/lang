package arm64ssa_test

import (
	"regexp"
	"strings"
	"testing"

	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
)

// End to end, a call result reaches its home with no copies in between. It used
// to take three: capture into a scratch, place into the emit layer's staging
// register, then place into the home.
func TestCallResultReachesItsHomeWithoutCopies(t *testing.T) {
	asm, err := arm64ssa.EmitAsmModule(crossCallModule(), "main", arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	body := funcText(t, asm, "main")

	moves := deliveryMoves(t, body)
	// Nothing may stage through the scratch pool (x12..x15) on the way back.
	staging := regexp.MustCompile(`\bx1[2-5]\b`)
	for _, m := range moves {
		if staging.MatchString(m) {
			t.Errorf("call result staged through the scratch pool: %q\n%s", m, body)
		}
	}
	if len(moves) > 1 {
		t.Errorf("call result took %d movs to reach its home, want at most 1: %q\n%s", len(moves), moves, body)
	}
}

// deliveryMoves returns the contiguous run of `mov` instructions immediately
// after the call — the sequence that carries the result out of x0. On the
// direct path that is one move, or none when the home already IS x0; the
// staged path adds a capture into scratch and a place out of it.
func deliveryMoves(t *testing.T, body string) []string {
	t.Helper()
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if strings.Contains(l, "bl fn_") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no call in body:\n%s", body)
	}
	var out []string
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "mov ") {
			break
		}
		out = append(out, trimmed)
	}
	return out
}
