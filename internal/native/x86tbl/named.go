package x86tbl

// NamedOp is one row of the by-name vocabulary: an instruction neither a
// group, the condition table nor the two-byte SSE table reaches, keyed on
// the spelling each assembler dispatches on.
//
// The two dialects name instructions differently — Intel writes one `lea`
// and reads the width off the operands, AT&T writes leaq/leal/leaw — so a
// row is one AT&T spelling together with the Intel mnemonic it maps to, and
// several rows share an Intel mnemonic. The encoding fields are what the
// family's generated Fern lookup returns for the spelling; the Go assembler
// reads the same fields off the first row of the Intel mnemonic.
type NamedOp struct {
	// ATT is the spelling examples/self_host/x86_native.fern dispatches on.
	// It is empty for the one Intel mnemonic whose AT&T spelling belongs to
	// another row: the SSE `movq`, which AT&T spells the same as the
	// suffixed general-register move and which x86_gas_movq resolves by
	// operand.
	ATT string
	// Suffixed marks a spelling the self-host matches after stripping the
	// AT&T size suffix (testq → test), the way the group families are
	// matched. The generated predicate is called with the stripped base.
	Suffixed bool
	// Intel is the mnemonic internal/native/x86_64 dispatches on.
	Intel string
	// Prefix, Op and Ext are the encoding data the family's lookup carries;
	// each family documents how it packs them. Ext doubles as the width or
	// size field where a family needs one.
	Prefix, Op, Ext byte
	// Probe and ATTProbe are one representative instruction in each
	// dialect. They are the test inventory: the native assembler must accept
	// Probe, and the self-host assembler must encode ATTProbe to the same
	// bytes.
	Probe, ATTProbe string
}

// NamedFamily groups the rows one encoder arm takes on each side.
type NamedFamily struct {
	Name string
	Doc  string
	// FernFn is the generated Fern lookup, "" for a family that needs only
	// the predicate. Pack says what it returns.
	FernFn string
	Pack   func(NamedOp) int
	Ops    []NamedOp
}

// PredicateName is the generated Fern predicate over the family's spellings.
func (f NamedFamily) PredicateName() string { return "x86_gas_is_" + f.Name }

func pfxOp(o NamedOp) int    { return int(o.Prefix)*256 + int(o.Op) }
func opOnly(o NamedOp) int   { return int(o.Op) }
func pfxOnly(o NamedOp) int  { return int(o.Prefix) }
func extOp(o NamedOp) int    { return int(o.Ext)*256 + int(o.Op) }
func extOnly(o NamedOp) int  { return int(o.Ext) }
func pfxExt(o NamedOp) int   { return int(o.Prefix)*256 + int(o.Ext) }
func pfxOpExt(o NamedOp) int { return int(o.Prefix)*65536 + int(o.Op)*256 + int(o.Ext) }

// Named is the by-name vocabulary of both x86-64 assemblers.
var Named = []NamedFamily{
	{Name: "rep", Doc: "the repeat prefixes; Prefix is the byte — rep/repe/repz are one F3, repne/repnz the F2, since the condition only means anything on cmps/scas", FernFn: "x86_gas_rep_pfx", Pack: pfxOnly, Ops: []NamedOp{
		{ATT: "rep", Intel: "rep", Prefix: 0xF3, Probe: "rep movsb", ATTProbe: "rep movsb"},
		{ATT: "repe", Intel: "repe", Prefix: 0xF3, Probe: "repe cmpsb", ATTProbe: "repe cmpsb"},
		{ATT: "repz", Intel: "repz", Prefix: 0xF3, Probe: "repz cmpsb", ATTProbe: "repz cmpsb"},
		{ATT: "repne", Intel: "repne", Prefix: 0xF2, Probe: "repne scasb", ATTProbe: "repne scasb"},
		{ATT: "repnz", Intel: "repnz", Prefix: 0xF2, Probe: "repnz scasb", ATTProbe: "repnz scasb"},
	}},
	{Name: "lock", Doc: "the lock prefix, validated against Lockable", Ops: []NamedOp{
		{ATT: "lock", Intel: "lock", Probe: "lock add qword ptr [rdi], 1", ATTProbe: "lock addq $1, (%rdi)"},
	}},
	{Name: "branch", Doc: "call and jmp: a label, or `*` for an indirect target", Ops: []NamedOp{
		{ATT: "call", Intel: "call", Probe: "call rax", ATTProbe: "call *%rax"},
		{ATT: "jmp", Intel: "jmp", Probe: "jmp rdx", ATTProbe: "jmp *%rdx"},
	}},
	{Name: "pushpop", Doc: "push and pop: a register, memory, or (push) an immediate; Op is the register-form opcode base", Ops: []NamedOp{
		{ATT: "pushq", Intel: "push", Op: 0x50, Probe: "push rbp", ATTProbe: "pushq %rbp"},
		{ATT: "popq", Intel: "pop", Op: 0x58, Probe: "pop rbp", ATTProbe: "popq %rbp"},
	}},
	{Name: "lea", Doc: "lea at every width AT&T spells; Ext is the destination size", FernFn: "x86_gas_lea_size", Pack: extOnly, Ops: []NamedOp{
		{ATT: "leaq", Intel: "lea", Ext: 64, Probe: "lea rax, [rbp-8]", ATTProbe: "leaq -8(%rbp), %rax"},
		{ATT: "leal", Intel: "lea", Ext: 32, Probe: "lea eax, [rbp-8]", ATTProbe: "leal -8(%rbp), %eax"},
		{ATT: "leaw", Intel: "lea", Ext: 16, Probe: "lea ax, [rbp-8]", ATTProbe: "leaw -8(%rbp), %ax"},
	}},
	{Name: "mov", Doc: "the general-register move at every width; Ext is the operand size. AT&T's movq is also the SSE movq, which x86_gas_movq tells apart by operand", FernFn: "x86_gas_mov_size", Pack: extOnly, Ops: []NamedOp{
		{ATT: "movb", Intel: "mov", Ext: 8, Probe: "mov byte ptr [rax], cl", ATTProbe: "movb %cl, (%rax)"},
		{ATT: "movw", Intel: "mov", Ext: 16, Probe: "mov ax, cx", ATTProbe: "movw %cx, %ax"},
		{ATT: "movl", Intel: "mov", Ext: 32, Probe: "mov eax, ecx", ATTProbe: "movl %ecx, %eax"},
		{ATT: "movq", Intel: "mov", Ext: 64, Probe: "mov rax, rcx", ATTProbe: "movq %rcx, %rax"},
	}},
	{Name: "movabs", Doc: "the 64-bit immediate move", Ops: []NamedOp{
		{ATT: "movabsq", Intel: "movabs", Probe: "movabs rax, 4294967296", ATTProbe: "movabsq $4294967296, %rax"},
		{ATT: "movabs", Intel: "movabs", Probe: "movabs rax, 4294967296", ATTProbe: "movabs $4294967296, %rax"},
	}},
	{Name: "extend", Doc: "the zero- and sign-extending loads. Op is B6/B7 (movzx) or BE/BF (movsx) — a byte or word SOURCE — and Ext the destination size AT&T names in the suffix's second letter; packed as op*256 + size/8",
		FernFn: "x86_gas_extend_op", Pack: func(o NamedOp) int { return int(o.Op)*256 + int(o.Ext)/8 }, Ops: []NamedOp{
			{ATT: "movzbw", Intel: "movzx", Op: 0xB6, Ext: 16, Probe: "movzx ax, byte ptr [rdi]", ATTProbe: "movzbw (%rdi), %ax"},
			{ATT: "movzbl", Intel: "movzx", Op: 0xB6, Ext: 32, Probe: "movzx eax, byte ptr [rdi]", ATTProbe: "movzbl (%rdi), %eax"},
			{ATT: "movzbq", Intel: "movzx", Op: 0xB6, Ext: 64, Probe: "movzx rax, byte ptr [rdi]", ATTProbe: "movzbq (%rdi), %rax"},
			{ATT: "movzwl", Intel: "movzx", Op: 0xB7, Ext: 32, Probe: "movzx eax, word ptr [rdi]", ATTProbe: "movzwl (%rdi), %eax"},
			{ATT: "movzwq", Intel: "movzx", Op: 0xB7, Ext: 64, Probe: "movzx rax, word ptr [rdi]", ATTProbe: "movzwq (%rdi), %rax"},
			{ATT: "movsbw", Intel: "movsx", Op: 0xBE, Ext: 16, Probe: "movsx cx, al", ATTProbe: "movsbw %al, %cx"},
			{ATT: "movsbl", Intel: "movsx", Op: 0xBE, Ext: 32, Probe: "movsx ecx, al", ATTProbe: "movsbl %al, %ecx"},
			{ATT: "movsbq", Intel: "movsx", Op: 0xBE, Ext: 64, Probe: "movsx rcx, al", ATTProbe: "movsbq %al, %rcx"},
			{ATT: "movswl", Intel: "movsx", Op: 0xBF, Ext: 32, Probe: "movsx ecx, ax", ATTProbe: "movswl %ax, %ecx"},
			{ATT: "movswq", Intel: "movsx", Op: 0xBF, Ext: 64, Probe: "movsx rcx, ax", ATTProbe: "movswq %ax, %rcx"},
		}},
	{Name: "movsxd", Doc: "the sign-extending 32-to-64 load (REX.W 63 /r); gas takes both spellings in AT&T", Ops: []NamedOp{
		{ATT: "movslq", Intel: "movsxd", Probe: "movsxd rax, ecx", ATTProbe: "movslq %ecx, %rax"},
		{ATT: "movsxd", Intel: "movsxd", Probe: "movsxd rax, ecx", ATTProbe: "movsxd %ecx, %rax"},
	}},
	{Name: "test", Doc: "test, at the suffix's width", Ops: []NamedOp{
		{ATT: "test", Suffixed: true, Intel: "test", Probe: "test rax, rcx", ATTProbe: "testq %rcx, %rax"},
	}},
	{Name: "imul", Doc: "imul in its one-, two- and three-operand forms", Ops: []NamedOp{
		{ATT: "imul", Suffixed: true, Intel: "imul", Probe: "imul rcx", ATTProbe: "imulq %rcx"},
	}},
	{Name: "bitscan", Doc: "the bit scans and counts: [Prefix] [REX.W] 0F Op /r, Ext the REX.W bit; packed as pfx*65536 + op*256 + w",
		FernFn: "x86_gas_bitscan_op", Pack: pfxOpExt, Ops: []NamedOp{
			{ATT: "bsfl", Intel: "bsf", Op: 0xBC, Ext: 0, Probe: "bsf eax, ecx", ATTProbe: "bsfl %ecx, %eax"},
			{ATT: "bsfq", Intel: "bsf", Op: 0xBC, Ext: 1, Probe: "bsf rax, rcx", ATTProbe: "bsfq %rcx, %rax"},
			{ATT: "bsrl", Intel: "bsr", Op: 0xBD, Ext: 0, Probe: "bsr eax, ecx", ATTProbe: "bsrl %ecx, %eax"},
			{ATT: "bsrq", Intel: "bsr", Op: 0xBD, Ext: 1, Probe: "bsr rax, rcx", ATTProbe: "bsrq %rcx, %rax"},
			{ATT: "lzcntl", Intel: "lzcnt", Prefix: 0xF3, Op: 0xBD, Ext: 0, Probe: "lzcnt eax, ecx", ATTProbe: "lzcntl %ecx, %eax"},
			{ATT: "lzcntq", Intel: "lzcnt", Prefix: 0xF3, Op: 0xBD, Ext: 1, Probe: "lzcnt rax, rcx", ATTProbe: "lzcntq %rcx, %rax"},
			{ATT: "tzcntl", Intel: "tzcnt", Prefix: 0xF3, Op: 0xBC, Ext: 0, Probe: "tzcnt eax, ecx", ATTProbe: "tzcntl %ecx, %eax"},
			{ATT: "tzcntq", Intel: "tzcnt", Prefix: 0xF3, Op: 0xBC, Ext: 1, Probe: "tzcnt rax, rcx", ATTProbe: "tzcntq %rcx, %rax"},
			{ATT: "popcntl", Intel: "popcnt", Prefix: 0xF3, Op: 0xB8, Ext: 0, Probe: "popcnt eax, ecx", ATTProbe: "popcntl %ecx, %eax"},
			{ATT: "popcntq", Intel: "popcnt", Prefix: 0xF3, Op: 0xB8, Ext: 1, Probe: "popcnt rax, rcx", ATTProbe: "popcntq %rcx, %rax"},
		}},
	{Name: "shld", Doc: "the double-precision shifts: 0F Op ib by immediate, 0F Op+1 by cl", FernFn: "x86_gas_shld_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "shld", Suffixed: true, Intel: "shld", Op: 0xA4, Probe: "shld rsi, rdi, cl", ATTProbe: "shldq %cl, %rdi, %rsi"},
		{ATT: "shrd", Suffixed: true, Intel: "shrd", Op: 0xAC, Probe: "shrd rsi, rdi, 5", ATTProbe: "shrdq $5, %rdi, %rsi"},
	}},
	{Name: "bswap", Doc: "byte swap, 32- and 64-bit registers only", Ops: []NamedOp{
		{ATT: "bswap", Suffixed: true, Intel: "bswap", Probe: "bswap rax", ATTProbe: "bswapq %rax"},
	}},
	{Name: "rmw", Doc: "the read-modify-write exchanges: 0F Op /r for the byte width, 0F Op+1 otherwise", FernFn: "x86_gas_rmw_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "xadd", Suffixed: true, Intel: "xadd", Op: 0xC0, Probe: "xadd rax, rcx", ATTProbe: "xaddq %rcx, %rax"},
		{ATT: "cmpxchg", Suffixed: true, Intel: "cmpxchg", Op: 0xB0, Probe: "cmpxchg qword ptr [rdi], rcx", ATTProbe: "cmpxchgq %rcx, (%rdi)"},
	}},
	{Name: "xchg", Doc: "exchange, register or memory on either side", Ops: []NamedOp{
		{ATT: "xchg", Suffixed: true, Intel: "xchg", Probe: "xchg qword ptr [rdi], rcx", ATTProbe: "xchgq %rcx, (%rdi)"},
	}},
	{Name: "crc32", Doc: "crc32 with the source width in the suffix", Ops: []NamedOp{
		{ATT: "crc32", Suffixed: true, Intel: "crc32", Probe: "crc32 eax, cl", ATTProbe: "crc32b %cl, %eax"},
	}},

	{Name: "movqd", Doc: "the GPR/xmm moves; direction from which operand is the xmm. AT&T's movq spelling is the mov family's, resolved there", Ops: []NamedOp{
		{ATT: "", Intel: "movq", Probe: "movq xmm0, rax", ATTProbe: "movq %rax, %xmm0"},
		{ATT: "movd", Intel: "movd", Probe: "movd xmm0, eax", ATTProbe: "movd %eax, %xmm0"},
	}},
	{Name: "mov10", Doc: "the 0F 10/11 move family; Prefix is the mandatory prefix, 0 for movups", FernFn: "x86_gas_mov10_pfx", Pack: pfxOnly, Ops: []NamedOp{
		{ATT: "movsd", Intel: "movsd", Prefix: 0xF2, Probe: "movsd xmm0, qword ptr [rdi]", ATTProbe: "movsd (%rdi), %xmm0"},
		{ATT: "movss", Intel: "movss", Prefix: 0xF3, Probe: "movss xmm1, xmm2", ATTProbe: "movss %xmm2, %xmm1"},
		{ATT: "movups", Intel: "movups", Prefix: 0x00, Probe: "movups xmm0, [rdi]", ATTProbe: "movups (%rdi), %xmm0"},
		{ATT: "movupd", Intel: "movupd", Prefix: 0x66, Probe: "movupd [rdi], xmm0", ATTProbe: "movupd %xmm0, (%rdi)"},
	}},
	{Name: "movdq", Doc: "the 16-byte moves: 0F 6F load, 0F 7F store, direction from which side is memory", FernFn: "x86_gas_movdq_pfx", Pack: pfxOnly, Ops: []NamedOp{
		{ATT: "movdqu", Intel: "movdqu", Prefix: 0xF3, Probe: "movdqu xmm0, [rdi]", ATTProbe: "movdqu (%rdi), %xmm0"},
		{ATT: "movdqa", Intel: "movdqa", Prefix: 0x66, Probe: "movdqa [rdi], xmm0", ATTProbe: "movdqa %xmm0, (%rdi)"},
	}},
	{Name: "cvt2s", Doc: "integer to scalar float: Prefix 0F 2A, Ext the REX.W bit the AT&T suffix names; packed as pfx*256 + w",
		FernFn: "x86_gas_cvt2s_op", Pack: pfxExt, Ops: []NamedOp{
			{ATT: "cvtsi2sd", Intel: "cvtsi2sd", Prefix: 0xF2, Ext: 1, Probe: "cvtsi2sd xmm0, rax", ATTProbe: "cvtsi2sd %rax, %xmm0"},
			{ATT: "cvtsi2sdq", Intel: "cvtsi2sd", Prefix: 0xF2, Ext: 1, Probe: "cvtsi2sd xmm0, rax", ATTProbe: "cvtsi2sdq %rax, %xmm0"},
			{ATT: "cvtsi2sdl", Intel: "cvtsi2sd", Prefix: 0xF2, Ext: 0, Probe: "cvtsi2sd xmm0, eax", ATTProbe: "cvtsi2sdl %eax, %xmm0"},
			{ATT: "cvtsi2ss", Intel: "cvtsi2ss", Prefix: 0xF3, Ext: 1, Probe: "cvtsi2ss xmm1, rax", ATTProbe: "cvtsi2ss %rax, %xmm1"},
			{ATT: "cvtsi2ssq", Intel: "cvtsi2ss", Prefix: 0xF3, Ext: 1, Probe: "cvtsi2ss xmm1, rax", ATTProbe: "cvtsi2ssq %rax, %xmm1"},
			{ATT: "cvtsi2ssl", Intel: "cvtsi2ss", Prefix: 0xF3, Ext: 0, Probe: "cvtsi2ss xmm1, eax", ATTProbe: "cvtsi2ssl %eax, %xmm1"},
		}},
	{Name: "cvt2si", Doc: "scalar float to integer: Prefix 0F Op, 2C truncating and 2D rounding; the AT&T l/q suffix names the width the destination register carries; packed as pfx*256 + op",
		FernFn: "x86_gas_cvt2si", Pack: pfxOp, Ops: []NamedOp{
			{ATT: "cvttsd2si", Intel: "cvttsd2si", Prefix: 0xF2, Op: 0x2C, Probe: "cvttsd2si eax, xmm1", ATTProbe: "cvttsd2si %xmm1, %eax"},
			{ATT: "cvttsd2sil", Intel: "cvttsd2si", Prefix: 0xF2, Op: 0x2C, Probe: "cvttsd2si eax, xmm1", ATTProbe: "cvttsd2sil %xmm1, %eax"},
			{ATT: "cvttsd2siq", Intel: "cvttsd2si", Prefix: 0xF2, Op: 0x2C, Probe: "cvttsd2si rax, xmm1", ATTProbe: "cvttsd2siq %xmm1, %rax"},
			{ATT: "cvtsd2si", Intel: "cvtsd2si", Prefix: 0xF2, Op: 0x2D, Probe: "cvtsd2si eax, xmm1", ATTProbe: "cvtsd2si %xmm1, %eax"},
			{ATT: "cvtsd2sil", Intel: "cvtsd2si", Prefix: 0xF2, Op: 0x2D, Probe: "cvtsd2si eax, xmm1", ATTProbe: "cvtsd2sil %xmm1, %eax"},
			{ATT: "cvtsd2siq", Intel: "cvtsd2si", Prefix: 0xF2, Op: 0x2D, Probe: "cvtsd2si rax, xmm1", ATTProbe: "cvtsd2siq %xmm1, %rax"},
			{ATT: "cvttss2si", Intel: "cvttss2si", Prefix: 0xF3, Op: 0x2C, Probe: "cvttss2si eax, xmm1", ATTProbe: "cvttss2si %xmm1, %eax"},
			{ATT: "cvttss2sil", Intel: "cvttss2si", Prefix: 0xF3, Op: 0x2C, Probe: "cvttss2si eax, xmm1", ATTProbe: "cvttss2sil %xmm1, %eax"},
			{ATT: "cvttss2siq", Intel: "cvttss2si", Prefix: 0xF3, Op: 0x2C, Probe: "cvttss2si rax, xmm1", ATTProbe: "cvttss2siq %xmm1, %rax"},
			{ATT: "cvtss2si", Intel: "cvtss2si", Prefix: 0xF3, Op: 0x2D, Probe: "cvtss2si eax, xmm1", ATTProbe: "cvtss2si %xmm1, %eax"},
			{ATT: "cvtss2sil", Intel: "cvtss2si", Prefix: 0xF3, Op: 0x2D, Probe: "cvtss2si eax, xmm1", ATTProbe: "cvtss2sil %xmm1, %eax"},
			{ATT: "cvtss2siq", Intel: "cvtss2si", Prefix: 0xF3, Op: 0x2D, Probe: "cvtss2si rax, xmm1", ATTProbe: "cvtss2siq %xmm1, %rax"},
		}},
	{Name: "imm3a", Doc: "the 66 0F 3A Op /r ib three-operand forms with an xmm destination", FernFn: "x86_gas_imm3a_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "roundss", Intel: "roundss", Op: 0x0A, Probe: "roundss xmm0, xmm1, 0", ATTProbe: "roundss $0, %xmm1, %xmm0"},
		{ATT: "roundsd", Intel: "roundsd", Op: 0x0B, Probe: "roundsd xmm0, xmm1, 0", ATTProbe: "roundsd $0, %xmm1, %xmm0"},
		{ATT: "pcmpestri", Intel: "pcmpestri", Op: 0x61, Probe: "pcmpestri xmm0, xmm1, 0", ATTProbe: "pcmpestri $0, %xmm1, %xmm0"},
		{ATT: "pcmpistri", Intel: "pcmpistri", Op: 0x63, Probe: "pcmpistri xmm0, xmm1, 0", ATTProbe: "pcmpistri $0, %xmm1, %xmm0"},
	}},
	{Name: "shuf", Doc: "the [Prefix] 0F Op /r ib shuffles; packed as pfx*256 + op", FernFn: "x86_gas_shuf_op", Pack: pfxOp, Ops: []NamedOp{
		{ATT: "pshufd", Intel: "pshufd", Prefix: 0x66, Op: 0x70, Probe: "pshufd xmm1, xmm2, 0", ATTProbe: "pshufd $0, %xmm2, %xmm1"},
		{ATT: "shufps", Intel: "shufps", Prefix: 0x00, Op: 0xC6, Probe: "shufps xmm1, xmm2, 0", ATTProbe: "shufps $0, %xmm2, %xmm1"},
		{ATT: "shufpd", Intel: "shufpd", Prefix: 0x66, Op: 0xC6, Probe: "shufpd xmm1, xmm2, 1", ATTProbe: "shufpd $1, %xmm2, %xmm1"},
	}},
	{Name: "pextr", Doc: "lane extract into a GPR or memory", Ops: []NamedOp{
		{ATT: "pextrb", Intel: "pextrb", Probe: "pextrb eax, xmm1, 0", ATTProbe: "pextrb $0, %xmm1, %eax"},
		{ATT: "pextrw", Intel: "pextrw", Probe: "pextrw eax, xmm1, 0", ATTProbe: "pextrw $0, %xmm1, %eax"},
		{ATT: "pextrd", Intel: "pextrd", Probe: "pextrd eax, xmm1, 0", ATTProbe: "pextrd $0, %xmm1, %eax"},
		{ATT: "pextrq", Intel: "pextrq", Probe: "pextrq rax, xmm1, 0", ATTProbe: "pextrq $0, %xmm1, %rax"},
	}},
	{Name: "pinsr", Doc: "lane insert from a GPR or memory", Ops: []NamedOp{
		{ATT: "pinsrb", Intel: "pinsrb", Probe: "pinsrb xmm1, eax, 0", ATTProbe: "pinsrb $0, %eax, %xmm1"},
		{ATT: "pinsrw", Intel: "pinsrw", Probe: "pinsrw xmm1, eax, 0", ATTProbe: "pinsrw $0, %eax, %xmm1"},
		{ATT: "pinsrd", Intel: "pinsrd", Probe: "pinsrd xmm1, eax, 0", ATTProbe: "pinsrd $0, %eax, %xmm1"},
		{ATT: "pinsrq", Intel: "pinsrq", Probe: "pinsrq xmm1, rax, 0", ATTProbe: "pinsrq $0, %rax, %xmm1"},
	}},
	{Name: "movmsk", Doc: "the sign-bit gathers into a GPR: [Prefix] 0F Op /r; packed as pfx*256 + op", FernFn: "x86_gas_movmsk_op", Pack: pfxOp, Ops: []NamedOp{
		{ATT: "pmovmskb", Intel: "pmovmskb", Prefix: 0x66, Op: 0xD7, Probe: "pmovmskb eax, xmm0", ATTProbe: "pmovmskb %xmm0, %eax"},
		{ATT: "movmskps", Intel: "movmskps", Prefix: 0x00, Op: 0x50, Probe: "movmskps eax, xmm0", ATTProbe: "movmskps %xmm0, %eax"},
		{ATT: "movmskpd", Intel: "movmskpd", Prefix: 0x66, Op: 0x50, Probe: "movmskpd eax, xmm0", ATTProbe: "movmskpd %xmm0, %eax"},
	}},
	{Name: "vshift", Doc: "the vector shifts BY IMMEDIATE, the 66 0F Op /Ext ib groups; the by-register spellings are the SSE table's. pslldq/psrldq exist only here; packed as ext*256 + op",
		FernFn: "x86_gas_vshift_op", Pack: extOp, Ops: []NamedOp{
			{ATT: "psrlw", Intel: "psrlw", Op: 0x71, Ext: 2, Probe: "psrlw xmm0, 3", ATTProbe: "psrlw $3, %xmm0"},
			{ATT: "psraw", Intel: "psraw", Op: 0x71, Ext: 4, Probe: "psraw xmm0, 3", ATTProbe: "psraw $3, %xmm0"},
			{ATT: "psllw", Intel: "psllw", Op: 0x71, Ext: 6, Probe: "psllw xmm0, 3", ATTProbe: "psllw $3, %xmm0"},
			{ATT: "psrld", Intel: "psrld", Op: 0x72, Ext: 2, Probe: "psrld xmm0, 3", ATTProbe: "psrld $3, %xmm0"},
			{ATT: "psrad", Intel: "psrad", Op: 0x72, Ext: 4, Probe: "psrad xmm0, 3", ATTProbe: "psrad $3, %xmm0"},
			{ATT: "pslld", Intel: "pslld", Op: 0x72, Ext: 6, Probe: "pslld xmm0, 3", ATTProbe: "pslld $3, %xmm0"},
			{ATT: "psrlq", Intel: "psrlq", Op: 0x73, Ext: 2, Probe: "psrlq xmm0, 3", ATTProbe: "psrlq $3, %xmm0"},
			{ATT: "psllq", Intel: "psllq", Op: 0x73, Ext: 6, Probe: "psllq xmm0, 3", ATTProbe: "psllq $3, %xmm0"},
			{ATT: "psrldq", Intel: "psrldq", Op: 0x73, Ext: 3, Probe: "psrldq xmm0, 8", ATTProbe: "psrldq $8, %xmm0"},
			{ATT: "pslldq", Intel: "pslldq", Op: 0x73, Ext: 7, Probe: "pslldq xmm0, 8", ATTProbe: "pslldq $8, %xmm0"},
		}},
	{Name: "sse38", Doc: "the 66 0F 38 Op /r forms with an xmm destination (SSE4.1, inside the Haswell baseline)", FernFn: "x86_gas_sse38_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "ptest", Intel: "ptest", Op: 0x17, Probe: "ptest xmm0, xmm1", ATTProbe: "ptest %xmm1, %xmm0"},
		{ATT: "pmulld", Intel: "pmulld", Op: 0x40, Probe: "pmulld xmm0, xmm1", ATTProbe: "pmulld %xmm1, %xmm0"},
		{ATT: "pminsb", Intel: "pminsb", Op: 0x38, Probe: "pminsb xmm0, xmm1", ATTProbe: "pminsb %xmm1, %xmm0"},
		{ATT: "pminsd", Intel: "pminsd", Op: 0x39, Probe: "pminsd xmm0, xmm1", ATTProbe: "pminsd %xmm1, %xmm0"},
		{ATT: "pminuw", Intel: "pminuw", Op: 0x3A, Probe: "pminuw xmm0, xmm1", ATTProbe: "pminuw %xmm1, %xmm0"},
		{ATT: "pminud", Intel: "pminud", Op: 0x3B, Probe: "pminud xmm0, xmm1", ATTProbe: "pminud %xmm1, %xmm0"},
		{ATT: "pmaxsb", Intel: "pmaxsb", Op: 0x3C, Probe: "pmaxsb xmm0, xmm1", ATTProbe: "pmaxsb %xmm1, %xmm0"},
		{ATT: "pmaxsd", Intel: "pmaxsd", Op: 0x3D, Probe: "pmaxsd xmm0, xmm1", ATTProbe: "pmaxsd %xmm1, %xmm0"},
		{ATT: "pmaxuw", Intel: "pmaxuw", Op: 0x3E, Probe: "pmaxuw xmm0, xmm1", ATTProbe: "pmaxuw %xmm1, %xmm0"},
		{ATT: "pmaxud", Intel: "pmaxud", Op: 0x3F, Probe: "pmaxud xmm0, xmm1", ATTProbe: "pmaxud %xmm1, %xmm0"},
	}},
}

// namedByIntel maps an Intel mnemonic to its family and first row.
var namedByIntel = func() map[string][2]int {
	m := map[string][2]int{}
	for fi, f := range Named {
		for oi, o := range f.Ops {
			if _, seen := m[o.Intel]; !seen {
				m[o.Intel] = [2]int{fi, oi}
			}
		}
	}
	return m
}()

// NamedByIntel looks an Intel mnemonic up; ok is false when no family lists
// it. The row is the first one for the mnemonic, whose prefix and opcode
// are the ones every width shares.
func NamedByIntel(mnem string) (fam *NamedFamily, op *NamedOp, ok bool) {
	idx, found := namedByIntel[mnem]
	if !found {
		return nil, nil, false
	}
	return &Named[idx[0]], &Named[idx[0]].Ops[idx[1]], true
}

// NamedRows is one family's rows.
func NamedRows(family string) []NamedOp {
	for _, f := range Named {
		if f.Name == family {
			return f.Ops
		}
	}
	panic("x86tbl: no named family " + family)
}

// NamedIntelMnemonics is every Intel mnemonic the by-name families reach,
// in table order without repeats.
func NamedIntelMnemonics() []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range Named {
		for _, o := range f.Ops {
			if !seen[o.Intel] {
				seen[o.Intel] = true
				out = append(out, o.Intel)
			}
		}
	}
	return out
}
