package x86tbl

// FixedOp is an instruction reached by mnemonic alone — no operands, a fixed
// byte sequence — together with the spellings gas accepts for it and in which
// syntax mode.
//
// The mode split is the reason this is a table rather than two lists. gas
// resolves a mnemonic against ONE table for both syntax modes, so the rule is
// not "AT&T names in AT&T, Intel names under .intel_syntax": the AT&T
// sign-extend names (cbtw, cwtl, cltq, cwtd, cltd, cqto) assemble under
// .intel_syntax too, the Intel ones (cbw, cwde, cdqe, cwd, cdq, cqo) assemble
// under AT&T, and pushf/popf/pushfq/popfq are accepted everywhere.
//
// The string ops are the exception, and only in their DWORD form: stosl,
// lodsl and scasl are AT&T only, stosd, lodsd and scasd are Intel only — but
// movsd and cmpsd are accepted in BOTH, because those two spellings are also
// the SSE scalar-double mnemonics and gas carries them regardless of mode.
// Nothing about that is derivable from the dialect; it is why the two
// assemblers drifted here.
//
// Every row is pinned from `as --64` read back with objdump, never from a
// manual. TestFixedOpsMatchGNUAs re-derives the whole table from gas and
// fails on any row gas disagrees with, in either mode.
type FixedOp struct {
	// Both, Intel and ATT are the spellings gas accepts in both syntax
	// modes, under .intel_syntax only, and under AT&T only.
	Both, Intel, ATT []string
	Bytes            []byte
	// Repeatable marks what a rep/repne prefix may precede. That is the
	// string ops plus two idioms gas also takes: `rep ret`, the AMD
	// branch-prediction workaround, and `rep nop`, which IS pause. Anything
	// else is refused — gas says "invalid instruction after rep", and the
	// prefix would otherwise be emitted in front of an instruction that
	// ignores it.
	Repeatable bool
}

// FixedOps is the no-operand vocabulary of both x86-64 assemblers.
var FixedOps = []FixedOp{
	{Both: []string{"ret"}, Bytes: []byte{0xC3}, Repeatable: true},
	{Both: []string{"syscall"}, Bytes: []byte{0x0F, 0x05}},
	{Both: []string{"leave"}, Bytes: []byte{0xC9}},
	{Both: []string{"cld"}, Bytes: []byte{0xFC}},
	{Both: []string{"std"}, Bytes: []byte{0xFD}},
	{Both: []string{"nop"}, Bytes: []byte{0x90}, Repeatable: true},
	{Both: []string{"int3"}, Bytes: []byte{0xCC}},
	{Both: []string{"pause"}, Bytes: []byte{0xF3, 0x90}},
	{Both: []string{"mfence"}, Bytes: []byte{0x0F, 0xAE, 0xF0}},
	{Both: []string{"lfence"}, Bytes: []byte{0x0F, 0xAE, 0xE8}},
	{Both: []string{"sfence"}, Bytes: []byte{0x0F, 0xAE, 0xF8}},
	{Both: []string{"cbtw", "cbw"}, Bytes: []byte{0x66, 0x98}},
	{Both: []string{"cwtl", "cwde"}, Bytes: []byte{0x98}},
	{Both: []string{"cltq", "cdqe"}, Bytes: []byte{0x48, 0x98}},
	{Both: []string{"cwtd", "cwd"}, Bytes: []byte{0x66, 0x99}},
	{Both: []string{"cltd", "cdq"}, Bytes: []byte{0x99}},
	{Both: []string{"cqto", "cqo"}, Bytes: []byte{0x48, 0x99}},
	// In 64-bit mode the flags push is already 64 bits wide, so the `q` is
	// spelling rather than a REX.W and gas disassembles both back to pushf.
	{Both: []string{"pushfq", "pushf"}, Bytes: []byte{0x9C}},
	{Both: []string{"popfq", "popf"}, Bytes: []byte{0x9D}},
	// The architecturally-guaranteed invalid opcode: what a trap or an
	// unreachable lowers to.
	{Both: []string{"ud2"}, Bytes: []byte{0x0F, 0x0B}},

	{Both: []string{"movsb"}, Bytes: []byte{0xA4}, Repeatable: true},
	{Both: []string{"movsw"}, Bytes: []byte{0x66, 0xA5}, Repeatable: true},
	{Both: []string{"movsd"}, ATT: []string{"movsl"}, Bytes: []byte{0xA5}, Repeatable: true},
	{Both: []string{"movsq"}, Bytes: []byte{0x48, 0xA5}, Repeatable: true},
	{Both: []string{"stosb"}, Bytes: []byte{0xAA}, Repeatable: true},
	{Both: []string{"stosw"}, Bytes: []byte{0x66, 0xAB}, Repeatable: true},
	{Intel: []string{"stosd"}, ATT: []string{"stosl"}, Bytes: []byte{0xAB}, Repeatable: true},
	{Both: []string{"stosq"}, Bytes: []byte{0x48, 0xAB}, Repeatable: true},
	{Both: []string{"lodsb"}, Bytes: []byte{0xAC}, Repeatable: true},
	{Both: []string{"lodsw"}, Bytes: []byte{0x66, 0xAD}, Repeatable: true},
	{Intel: []string{"lodsd"}, ATT: []string{"lodsl"}, Bytes: []byte{0xAD}, Repeatable: true},
	{Both: []string{"lodsq"}, Bytes: []byte{0x48, 0xAD}, Repeatable: true},
	{Both: []string{"scasb"}, Bytes: []byte{0xAE}, Repeatable: true},
	{Both: []string{"scasw"}, Bytes: []byte{0x66, 0xAF}, Repeatable: true},
	{Intel: []string{"scasd"}, ATT: []string{"scasl"}, Bytes: []byte{0xAF}, Repeatable: true},
	{Both: []string{"scasq"}, Bytes: []byte{0x48, 0xAF}, Repeatable: true},
	{Both: []string{"cmpsb"}, Bytes: []byte{0xA6}, Repeatable: true},
	{Both: []string{"cmpsw"}, Bytes: []byte{0x66, 0xA7}, Repeatable: true},
	{Both: []string{"cmpsd"}, ATT: []string{"cmpsl"}, Bytes: []byte{0xA7}, Repeatable: true},
	{Both: []string{"cmpsq"}, Bytes: []byte{0x48, 0xA7}, Repeatable: true},
}

// IntelSpellings and ATTSpellings are the names gas accepts for the row in
// each syntax mode.
func (f FixedOp) IntelSpellings() []string { return append(append([]string{}, f.Both...), f.Intel...) }
func (f FixedOp) ATTSpellings() []string   { return append(append([]string{}, f.Both...), f.ATT...) }

// FixedOpMap is the Intel-syntax lookup internal/native/x86_64 assembles
// through: every spelling gas accepts under .intel_syntax, to its bytes.
func FixedOpMap() map[string][]byte {
	m := map[string][]byte{}
	for _, f := range FixedOps {
		for _, s := range f.IntelSpellings() {
			m[s] = f.Bytes
		}
	}
	return m
}

// RepeatableMnemonics is the set a rep/repne prefix may precede, in Intel
// spelling. Anything else is #UD at runtime, so it is refused at assembly.
func RepeatableMnemonics() map[string]bool {
	m := map[string]bool{}
	for _, f := range FixedOps {
		if !f.Repeatable {
			continue
		}
		for _, s := range f.IntelSpellings() {
			m[s] = true
		}
	}
	return m
}
