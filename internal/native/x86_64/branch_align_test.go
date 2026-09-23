package x86_64

import (
	"fmt"
	"strings"
	"testing"
)

// Under branch alignment no jump, call or return crosses a 32-byte boundary
// or ends on one, whatever precedes it, and every relaxed branch still lands
// on its label. The programs put each branch kind behind 0..40 bytes of
// filler so every offset modulo 32 is exercised, with a short and a long
// jump target on each.
func TestBranchAlignmentKeepsBranchesOffThirtyTwoByteLines(t *testing.T) {
	for _, base := range []uint64{0, 8, 0x4000e8, 31} {
		for fill := 0; fill <= 40; fill++ {
			checkBranchAlignment(t, base, fill)
		}
	}
}

func checkBranchAlignment(t *testing.T, base uint64, fill int) {
	t.Helper()
	{
		var sb strings.Builder
		sb.WriteString(".text\n_start:\n")
		for i := 0; i < fill; i++ {
			sb.WriteString("\tnop\n")
		}
		sb.WriteString("\tcmp rax, 1\n\tjne near\n\tjmp far\n\tcall far\n\tcall rax\n\tret\nnear:\n\tadd rax, 1\n\tjmp near\nfar:\n")
		for i := 0; i < 150; i++ {
			sb.WriteString("\tadd rcx, 1\n")
		}
		sb.WriteString("\tjb near\n\tret\n")
		a, err := ParseProgram(sb.String())
		if err != nil {
			t.Fatalf("fill %d: %v", fill, err)
		}
		a.SetBranchAlignment(base)
		// Relaxation drops the fixups it resolves inline, so the targets
		// are read off before the layout.
		target := make([]string, len(a.relaxEvents))
		for i, e := range a.relaxEvents {
			if e.align == 0 && !e.fixed {
				target[i] = a.relFixups[e.fixup].sym
			}
		}
		text, _, err := a.BytesProgram(base)
		if err != nil {
			t.Fatalf("fill %d: %v", fill, err)
		}
		branches := 0
		for i, e := range a.relaxEvents {
			if e.align > 0 {
				continue
			}
			branches++
			// The padding sits at the event's start, ahead of the lead
			// instruction the event reached back over (relaxEvent.lead), so
			// the branch itself begins past both.
			at := e.newStart + e.bpad + e.lead
			n := e.newSize - e.bpad - e.lead
			addr, last := int(base)+at, int(base)+at+n-1
			if addr/32 != last/32 || last%32 == 31 {
				t.Errorf("base %#x fill %d: a branch of %d bytes at address %#x (mod 32 = %d) crosses or ends on a 32-byte line", base, fill, n, addr, addr%32)
			}
			// A macro-fused pair is one span to the erratum, so putting the
			// jcc alone at a line start would leave the pair straddling it
			// exactly — the shape this is supposed to avoid.
			if e.leadFuse {
				fa, fl := int(base)+e.newStart+e.bpad, int(base)+e.newStart+e.bpad+e.lead+n-1
				if fa/32 != fl/32 || fl%32 == 31 {
					t.Errorf("base %#x fill %d: a fused %d-byte pair at address %#x (mod 32 = %d) crosses or ends on a 32-byte line", base, fill, e.lead+n, fa, fa%32)
				}
			}
			// Padding spent as prefixes must stay within one instruction's
			// encoding: 5 per GNU as, and the decoder's 15-byte limit.
			if e.bpfx > 0 {
				if e.bpfx > e.bpad || e.bpfx > 5 || e.lead+e.bpfx > 15 {
					t.Errorf("base %#x fill %d: %d prefixes on a %d-byte lead with %d bytes of pad", base, fill, e.bpfx, e.lead, e.bpad)
				}
				if !e.leadPrefixable {
					t.Errorf("base %#x fill %d: prefixes on a lead that cannot take them", base, fill)
				}
			}
			if e.fixed {
				continue
			}
			want := a.textLabels[target[i]]
			var got int
			switch text[at] {
			case 0xEB, 0x7F, 0x75, 0x72:
				got = at + 2 + int(int8(text[at+1]))
			case 0xE9:
				got = at + 5 + int(int32(uint32(text[at+1])|uint32(text[at+2])<<8|uint32(text[at+3])<<16|uint32(text[at+4])<<24))
			case 0x0F:
				got = at + 6 + int(int32(uint32(text[at+2])|uint32(text[at+3])<<8|uint32(text[at+4])<<16|uint32(text[at+5])<<24))
			default:
				t.Fatalf("fill %d: unexpected branch byte %#x at %#x", fill, text[at], at)
			}
			if got != want {
				t.Errorf("fill %d: the branch at %#x lands on %#x, its label is at %#x", fill, at, got, want)
			}
		}
		if branches != 8 {
			t.Errorf("fill %d: %d branch events, want 8", fill, branches)
		}
	}
}

// Off, the layout is the byte-exact one the GNU as oracle checks: nothing
// is padded.
func TestBranchAlignmentOffPadsNothing(t *testing.T) {
	src := ".text\n_start:\n" + strings.Repeat("\tnop\n", 30) + "\tjmp l\nl:\n\tret\n"
	text, _, err := AssembleProgram(src, 0x400000)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", append(append(make([]byte, 0), make([]byte, 30)...), 0xEB, 0x00, 0xC3)); strings.ReplaceAll(fmt.Sprintf("%x", text), "90", "00") != want {
		t.Errorf("text = % x", text)
	}
}

// The padding an aligned branch needs is spent on the instruction before it
// as redundant 2E prefixes wherever it fits, so it costs no instruction the
// loop retires (#10017). Before that, every one of these bytes was a NOP on
// the fall-through path.
//
// The fixture is the shape a scan loop is made of — a flag-setting ALU op
// then a conditional jump — walked across every start offset so the padding
// lands at every width a 32-byte line can ask for.
func TestBranchAlignmentSpendsPaddingAsPrefixes(t *testing.T) {
	converted, padded := 0, 0
	for fill := 0; fill <= 40; fill++ {
		var sb strings.Builder
		sb.WriteString(".text\n_start:\n")
		for i := 0; i < fill; i++ {
			sb.WriteString("\tnop\n")
		}
		for i := 0; i < 24; i++ {
			sb.WriteString("\tadd rcx, 1\n\tcmp rax, 1\n\tjne near\n")
		}
		sb.WriteString("near:\n\tret\n")
		a, err := ParseProgram(sb.String())
		if err != nil {
			t.Fatalf("fill %d: %v", fill, err)
		}
		a.SetBranchAlignment(0)
		text, _, err := a.BytesProgram(0)
		if err != nil {
			t.Fatalf("fill %d: %v", fill, err)
		}
		for _, e := range a.relaxEvents {
			if e.align > 0 || e.bpad == 0 {
				continue
			}
			padded++
			converted += e.bpfx
			// The prefixes sit at the event's start, after whatever NOPs the
			// lead could not absorb, and directly ahead of the lead's own
			// first byte — a 2E anywhere else is a different instruction.
			for k := 0; k < e.bpfx; k++ {
				if got := text[e.newStart+e.bpad-e.bpfx+k]; got != 0x2E {
					t.Fatalf("fill %d: byte %#x where a 2E prefix belongs", fill, got)
				}
			}
			if e.bpfx < e.bpad && e.lead+e.bpfx < 15 && e.bpfx < 5 {
				t.Errorf("fill %d: %d of %d pad bytes left as NOPs with room for more prefixes (lead %d)",
					fill, e.bpad-e.bpfx, e.bpad, e.lead)
			}
		}
	}
	if padded == 0 {
		t.Fatal("no branch needed padding; the case cannot show anything")
	}
	if converted == 0 {
		t.Errorf("%d padded branches and not one byte spent as a prefix", padded)
	}
	t.Logf("%d padded branches, %d pad bytes spent as prefixes", padded, converted)
}
