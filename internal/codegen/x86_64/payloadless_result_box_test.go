package x86_64

import (
	"strings"
	"testing"
)

// #8843. A helper in the I/O family builds its Option / Result box at the
// enum's UNIFORM size and the caller now owns and releases it (#8398 /
// #8405) — so its payloadless arm has a payload slot it never fills from a
// value. That slot must still be written: the IR's branchless enum drop
// releases a uniform enum's payload word WITHOUT testing the tag, so an
// unwritten slot reaches __fern_str_dec / __fern_rc_dec holding whatever
// the recycled block last contained, and rc-decrements an address the
// program never allocated. Nothing catches that on a native target; on
// wasm the same read traps out of bounds, which is how it was found.
//
// Each body below has a payloadless arm, so each must contain the explicit
// zero store. A `mov [rax + 8], <reg>` from a live value is the Some / Err
// arm and does not satisfy it.
func TestPayloadlessResultBoxArmsZeroTheirPayloadSlot(t *testing.T) {
	const zeroStore = "mov qword ptr [rax + 8], 0"

	probes := []struct {
		emit func(*generator)
		syms []string
	}{
		{(*generator).emitEnvRuntime, []string{"__fern_env"}},
		{(*generator).emitReadLineRuntime, []string{"__fern_read_line"}},
		{(*generator).emitReaderWriterRuntime, []string{
			"__fern_reader_read_line", "__fern_writer_write", "__fern_close_fd_box",
		}},
	}

	for _, p := range probes {
		g := &generator{syscalls: map[int]bool{}}
		p.emit(g)
		asm := g.out.String()
		for _, sym := range p.syms {
			body := helperBody(t, asm, sym)
			if !strings.Contains(body, zeroStore) {
				t.Errorf("%s has a payloadless arm that never writes the box's "+
					"payload slot: expected a %q. The uniform enum drop reads that "+
					"word without checking the tag, so it releases whatever the "+
					"recycled block held (#8843)\n--- body ---\n%s", sym, zeroStore, body)
			}
		}
	}
}
