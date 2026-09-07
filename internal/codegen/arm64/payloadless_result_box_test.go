package arm64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8843. A helper in the I/O family builds its Option / Result box at the
// enum's UNIFORM size and the caller now owns and releases it (#8398 /
// #8405) — so its payloadless arm has payload slots it never fills from a
// value. Those slots must still be written: the IR's branchless enum drop
// releases a uniform enum's payload words WITHOUT testing the tag, so an
// unwritten slot reaches __fern_str_dec / __fern_rc_dec holding whatever
// the recycled block last contained, and rc-decrements an address the
// program never allocated. Nothing catches that on a native target; on
// wasm the same read traps out of bounds, which is how it was found.
//
// A `str <reg>, [x0, #8]` from a live value is the Some / Err arm and does
// not satisfy this — only the zero-register store does. The two-word ABI is
// on, so an Option[string] box is 24 bytes and its len word at +16 needs
// the same treatment as the data word at +8.
func TestPayloadlessResultBoxArmsZeroTheirPayloadSlots(t *testing.T) {
	defer func(tw, rc bool) { ast.TwoWordOverride, ast.RcFreeEnabled = tw, rc }(ast.TwoWordOverride, ast.RcFreeEnabled)
	ast.TwoWordOverride = true
	ast.RcFreeEnabled = true

	probes := []struct {
		emit func(*generator)
		// want maps a helper to the zero stores its payloadless arm owes.
		want map[string][]string
	}{
		{(*generator).emitEnvRuntime, map[string][]string{
			"__fern_env": {"str xzr, [x0, #8]", "str xzr, [x0, #16]"},
		}},
		{(*generator).emitReadLineRuntime, map[string][]string{
			"__fern_read_line": {"str xzr, [x0, #8]", "str xzr, [x0, #16]"},
		}},
		{(*generator).emitReaderWriterRuntime, map[string][]string{
			"__fern_reader_read_line": {"str xzr, [x0, #8]", "str xzr, [x0, #16]"},
			// Option[IoError] is one pointer wide, so its box stays 16 bytes.
			"__fern_writer_write": {"str xzr, [x0, #8]"},
			"__fern_close_fd_box": {"str xzr, [x0, #8]"},
		}},
	}

	for _, p := range probes {
		g := &generator{stringLabel: map[string]string{}}
		p.emit(g)
		asm := g.out.String()
		for sym, stores := range p.want {
			body := helperBody(asm, sym)
			if body == "" {
				t.Fatalf("%s was not emitted; cannot check its payloadless arm", sym)
			}
			for _, s := range stores {
				if !strings.Contains(body, s) {
					t.Errorf("%s has a payloadless arm that never writes the box's "+
						"payload slot: expected a %q. The uniform enum drop reads that "+
						"word without checking the tag, so it releases whatever the "+
						"recycled block held (#8843)\n--- body ---\n%s", sym, s, body)
				}
			}
		}
	}
}
