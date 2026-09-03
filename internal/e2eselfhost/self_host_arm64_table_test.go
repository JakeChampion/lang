package e2eselfhost

import (
	"encoding/binary"
	"strings"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/arm64tbl"
)

// TestSelfHostArm64TableRowsMatchNative is the vocabulary gate between the
// two arm64 assemblers, read from the table both are built from (#7903).
//
// internal/native/arm64tbl.Scalar lists every mnemonic either assembler
// dispatches by name, with a representative instruction per row. The Go
// assembler routes on the family the table names; the self-host's
// predicates and arm64_gas_known are generated from the same rows
// (cmd/arm64tblgen's staleness test holds the committed output to it). So
// the SET of mnemonics cannot drift any more. What can still go wrong is a
// row the self-host's dispatch does not reach — the movn shape of #6060, a
// mnemonic in the allow-list with no arm to encode it, which drops the
// instruction silently — and that is what assembling every row through
// both sides and comparing the word catches.
//
// The gate this replaced read the mnemonic set out of the Fern source with
// a regular expression and probed a hand-kept list. It could not see a
// family dispatched by pattern, and its condition-alias exclusion
// `^b\.?[a-z]{2}$` also matched `brk` and `blr`, so `brk` — which native
// assembled and the self-host did not — sat unreported inside it.
func TestSelfHostArm64TableRowsMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	for _, fam := range arm64tbl.Scalar {
		for _, o := range fam.Ops {
			probe := fam.ProbeFor(o)
			src := ".text\n_start:\n" + probe + "\n"
			text, _, err := nativearm64.AssembleProgram(src, 0x400000)
			if err != nil {
				t.Errorf("%-32s internal/native/arm64 rejects its own table probe: %v", probe, err)
				continue
			}
			if o.Layout {
				// The word depends on where each assembler places the
				// sections; acceptance is what the row pins, and the
				// self-host's is checked without comparing.
				if refused := refusalsFor(t, bin, runner, src); len(refused) > 0 {
					t.Errorf("%-32s the self-host assembler REFUSES it (%s)", probe, strings.Join(refused, ", "))
				}
				continue
			}
			var want []uint32
			for i := 0; i+4 <= len(text); i += 4 {
				want = append(want, binary.LittleEndian.Uint32(text[i:]))
			}
			if refused := refusalsFor(t, bin, runner, src); len(refused) > 0 {
				t.Errorf("%-32s the self-host assembler REFUSES it (%s); native emits %08x", probe, strings.Join(refused, ", "), want)
				continue
			}
			got := assembleSelfHost(t, bin, runner, src)
			if len(got) != len(want) {
				t.Errorf("%-32s self-host produced %d words, native %d", probe, len(got), len(want))
				continue
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("%-32s word %d: self-host %08x, internal/native/arm64 %08x", probe, i, got[i], want[i])
				}
			}
		}
	}
}
