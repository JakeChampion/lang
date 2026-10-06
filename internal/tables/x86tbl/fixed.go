package x86tbl

// FixedOp is an instruction reached by mnemonic alone — no operands, a fixed
// byte sequence — together with every spelling gas accepts for it in AT&T
// mode.
//
// gas resolves a mnemonic against ONE table for both syntax modes, so AT&T
// mode takes the Intel sign-extend names (cbw, cwde, cdqe, cwd, cdq, cqo)
// beside the AT&T ones (cbtw, cwtl, cltq, cwtd, cltd, cqto), and movsd and
// cmpsd beside movsl and cmpsl, because those two are also the SSE
// scalar-double mnemonics. stosd, lodsd and scasd are the exception: Intel
// mode only.
//
// Every row is pinned from `as --64` read back with objdump, never from a
// manual, and TestSelfHostX86TableRowsMatchGas assembles every spelling
// through the self-host and GNU as.
type FixedOp struct {
	Spellings []string
	Bytes     []byte
	// Repeatable marks what a rep/repne prefix may precede. That is the
	// string ops plus two idioms gas also takes: `rep ret`, the AMD
	// branch-prediction workaround, and `rep nop`, which IS pause. Anything
	// else is refused — gas says "invalid instruction after rep", and the
	// prefix would otherwise be emitted in front of an instruction that
	// ignores it.
	Repeatable bool
}

// FixedOps is the no-operand vocabulary of the self-host x86-64 assembler.
var FixedOps = []FixedOp{
	{Spellings: []string{"ret"}, Bytes: []byte{0xC3}, Repeatable: true},
	{Spellings: []string{"syscall"}, Bytes: []byte{0x0F, 0x05}},
	{Spellings: []string{"leave"}, Bytes: []byte{0xC9}},
	{Spellings: []string{"cld"}, Bytes: []byte{0xFC}},
	{Spellings: []string{"std"}, Bytes: []byte{0xFD}},
	{Spellings: []string{"nop"}, Bytes: []byte{0x90}, Repeatable: true},
	{Spellings: []string{"int3"}, Bytes: []byte{0xCC}},
	{Spellings: []string{"pause"}, Bytes: []byte{0xF3, 0x90}},
	{Spellings: []string{"mfence"}, Bytes: []byte{0x0F, 0xAE, 0xF0}},
	{Spellings: []string{"lfence"}, Bytes: []byte{0x0F, 0xAE, 0xE8}},
	{Spellings: []string{"sfence"}, Bytes: []byte{0x0F, 0xAE, 0xF8}},
	{Spellings: []string{"cbtw", "cbw"}, Bytes: []byte{0x66, 0x98}},
	{Spellings: []string{"cwtl", "cwde"}, Bytes: []byte{0x98}},
	{Spellings: []string{"cltq", "cdqe"}, Bytes: []byte{0x48, 0x98}},
	{Spellings: []string{"cwtd", "cwd"}, Bytes: []byte{0x66, 0x99}},
	{Spellings: []string{"cltd", "cdq"}, Bytes: []byte{0x99}},
	{Spellings: []string{"cqto", "cqo"}, Bytes: []byte{0x48, 0x99}},
	// In 64-bit mode the flags push is already 64 bits wide, so the `q` is
	// spelling rather than a REX.W and gas disassembles both back to pushf.
	{Spellings: []string{"pushfq", "pushf"}, Bytes: []byte{0x9C}},
	{Spellings: []string{"popfq", "popf"}, Bytes: []byte{0x9D}},
	// The architecturally-guaranteed invalid opcode: what a trap or an
	// unreachable lowers to.
	{Spellings: []string{"ud2"}, Bytes: []byte{0x0F, 0x0B}},

	{Spellings: []string{"movsb"}, Bytes: []byte{0xA4}, Repeatable: true},
	{Spellings: []string{"movsw"}, Bytes: []byte{0x66, 0xA5}, Repeatable: true},
	{Spellings: []string{"movsd", "movsl"}, Bytes: []byte{0xA5}, Repeatable: true},
	{Spellings: []string{"movsq"}, Bytes: []byte{0x48, 0xA5}, Repeatable: true},
	{Spellings: []string{"stosb"}, Bytes: []byte{0xAA}, Repeatable: true},
	{Spellings: []string{"stosw"}, Bytes: []byte{0x66, 0xAB}, Repeatable: true},
	{Spellings: []string{"stosl"}, Bytes: []byte{0xAB}, Repeatable: true},
	{Spellings: []string{"stosq"}, Bytes: []byte{0x48, 0xAB}, Repeatable: true},
	{Spellings: []string{"lodsb"}, Bytes: []byte{0xAC}, Repeatable: true},
	{Spellings: []string{"lodsw"}, Bytes: []byte{0x66, 0xAD}, Repeatable: true},
	{Spellings: []string{"lodsl"}, Bytes: []byte{0xAD}, Repeatable: true},
	{Spellings: []string{"lodsq"}, Bytes: []byte{0x48, 0xAD}, Repeatable: true},
	{Spellings: []string{"scasb"}, Bytes: []byte{0xAE}, Repeatable: true},
	{Spellings: []string{"scasw"}, Bytes: []byte{0x66, 0xAF}, Repeatable: true},
	{Spellings: []string{"scasl"}, Bytes: []byte{0xAF}, Repeatable: true},
	{Spellings: []string{"scasq"}, Bytes: []byte{0x48, 0xAF}, Repeatable: true},
	{Spellings: []string{"cmpsb"}, Bytes: []byte{0xA6}, Repeatable: true},
	{Spellings: []string{"cmpsw"}, Bytes: []byte{0x66, 0xA7}, Repeatable: true},
	{Spellings: []string{"cmpsd", "cmpsl"}, Bytes: []byte{0xA7}, Repeatable: true},
	{Spellings: []string{"cmpsq"}, Bytes: []byte{0x48, 0xA7}, Repeatable: true},
}
