package x86tbl

// NamedOp is one row of the by-name vocabulary: an instruction neither a
// group, the condition table nor the two-byte SSE table reaches, keyed on
// the AT&T spelling the self-host dispatches on. The encoding fields are
// what the family's generated Fern lookup returns for the spelling.
type NamedOp struct {
	// ATT is the spelling examples/self_host/x86_native.fern dispatches on.
	// It is empty for the SSE `movq`, which AT&T spells the same as the
	// suffixed general-register move and which x86_gas_movq resolves by
	// operand.
	ATT string
	// Suffixed marks a spelling the self-host matches after stripping the
	// AT&T size suffix (testq → test), the way the group families are
	// matched. The generated predicate is called with the stripped base.
	Suffixed bool
	// Prefix, Op and Ext are the encoding data the family's lookup carries;
	// each family documents how it packs them. Ext doubles as the width or
	// size field where a family needs one.
	Prefix, Op, Ext byte
	// ATTProbe is one representative instruction: the test inventory the
	// self-host assembler must encode to GNU as's bytes.
	ATTProbe string
}

// NamedFamily groups the rows one encoder arm takes.
type NamedFamily struct {
	Name string
	Doc  string
	// FernFn is the generated Fern lookup, which a caller tests for >= 0 to
	// ask whether a mnemonic is in the family. A family without one generates
	// the predicate PredicateName instead. Pack says what the lookup returns.
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

// Named is the by-name vocabulary of the self-host x86-64 assembler.
var Named = []NamedFamily{
	{Name: "rep", Doc: "the repeat prefixes; Prefix is the byte — rep/repe/repz are one F3, repne/repnz the F2, since the condition only means anything on cmps/scas", FernFn: "x86_gas_rep_pfx", Pack: pfxOnly, Ops: []NamedOp{
		{ATT: "rep", Prefix: 0xF3, ATTProbe: "rep movsb"},
		{ATT: "repe", Prefix: 0xF3, ATTProbe: "repe cmpsb"},
		{ATT: "repz", Prefix: 0xF3, ATTProbe: "repz cmpsb"},
		{ATT: "repne", Prefix: 0xF2, ATTProbe: "repne scasb"},
		{ATT: "repnz", Prefix: 0xF2, ATTProbe: "repnz scasb"},
	}},
	{Name: "lock", Doc: "the lock prefix, validated against Lockable", Ops: []NamedOp{
		{ATT: "lock", ATTProbe: "lock addq $1, (%rdi)"},
	}},
	{Name: "branch", Doc: "call and jmp: a label, or `*` for an indirect target", Ops: []NamedOp{
		{ATT: "call", ATTProbe: "call *%rax"},
		{ATT: "jmp", ATTProbe: "jmp *%rdx"},
	}},
	{Name: "pushpop", Doc: "push and pop: a register, memory, or (push) an immediate; Op is the register-form opcode base", Ops: []NamedOp{
		{ATT: "pushq", Op: 0x50, ATTProbe: "pushq %rbp"},
		{ATT: "popq", Op: 0x58, ATTProbe: "popq %rbp"},
	}},
	{Name: "lea", Doc: "lea at every width AT&T spells; Ext is the destination size", FernFn: "x86_gas_lea_size", Pack: extOnly, Ops: []NamedOp{
		{ATT: "leaq", Ext: 64, ATTProbe: "leaq -8(%rbp), %rax"},
		{ATT: "leal", Ext: 32, ATTProbe: "leal -8(%rbp), %eax"},
		{ATT: "leaw", Ext: 16, ATTProbe: "leaw -8(%rbp), %ax"},
	}},
	{Name: "mov", Doc: "the general-register move at every width; Ext is the operand size. AT&T's movq is also the SSE movq, which x86_gas_movq tells apart by operand", FernFn: "x86_gas_mov_size", Pack: extOnly, Ops: []NamedOp{
		{ATT: "movb", Ext: 8, ATTProbe: "movb %cl, (%rax)"},
		{ATT: "movw", Ext: 16, ATTProbe: "movw %cx, %ax"},
		{ATT: "movl", Ext: 32, ATTProbe: "movl %ecx, %eax"},
		{ATT: "movq", Ext: 64, ATTProbe: "movq %rcx, %rax"},
	}},
	{Name: "movabs", Doc: "the 64-bit immediate move", Ops: []NamedOp{
		{ATT: "movabsq", ATTProbe: "movabsq $4294967296, %rax"},
		{ATT: "movabs", ATTProbe: "movabs $4294967296, %rax"},
	}},
	{Name: "extend", Doc: "the zero- and sign-extending loads. Op is B6/B7 (movzx) or BE/BF (movsx) — a byte or word SOURCE — and Ext the destination size AT&T names in the suffix's second letter; packed as op*256 + size/8",
		FernFn: "x86_gas_extend_op", Pack: func(o NamedOp) int { return int(o.Op)*256 + int(o.Ext)/8 }, Ops: []NamedOp{
			{ATT: "movzbw", Op: 0xB6, Ext: 16, ATTProbe: "movzbw (%rdi), %ax"},
			{ATT: "movzbl", Op: 0xB6, Ext: 32, ATTProbe: "movzbl (%rdi), %eax"},
			{ATT: "movzbq", Op: 0xB6, Ext: 64, ATTProbe: "movzbq (%rdi), %rax"},
			{ATT: "movzwl", Op: 0xB7, Ext: 32, ATTProbe: "movzwl (%rdi), %eax"},
			{ATT: "movzwq", Op: 0xB7, Ext: 64, ATTProbe: "movzwq (%rdi), %rax"},
			{ATT: "movsbw", Op: 0xBE, Ext: 16, ATTProbe: "movsbw %al, %cx"},
			{ATT: "movsbl", Op: 0xBE, Ext: 32, ATTProbe: "movsbl %al, %ecx"},
			{ATT: "movsbq", Op: 0xBE, Ext: 64, ATTProbe: "movsbq %al, %rcx"},
			{ATT: "movswl", Op: 0xBF, Ext: 32, ATTProbe: "movswl %ax, %ecx"},
			{ATT: "movswq", Op: 0xBF, Ext: 64, ATTProbe: "movswq %ax, %rcx"},
		}},
	{Name: "movsxd", Doc: "the sign-extending 32-to-64 load (REX.W 63 /r); gas takes both spellings in AT&T", Ops: []NamedOp{
		{ATT: "movslq", ATTProbe: "movslq %ecx, %rax"},
		{ATT: "movsxd", ATTProbe: "movsxd %ecx, %rax"},
	}},
	{Name: "test", Doc: "test, at the suffix's width", Ops: []NamedOp{
		{ATT: "test", Suffixed: true, ATTProbe: "testq %rcx, %rax"},
	}},
	{Name: "imul", Doc: "imul in its one-, two- and three-operand forms", Ops: []NamedOp{
		{ATT: "imul", Suffixed: true, ATTProbe: "imulq %rcx"},
	}},
	{Name: "bitscan", Doc: "the bit scans and counts: [Prefix] [REX.W] 0F Op /r, Ext the REX.W bit; packed as pfx*65536 + op*256 + w",
		FernFn: "x86_gas_bitscan_op", Pack: pfxOpExt, Ops: []NamedOp{
			{ATT: "bsfl", Op: 0xBC, Ext: 0, ATTProbe: "bsfl %ecx, %eax"},
			{ATT: "bsfq", Op: 0xBC, Ext: 1, ATTProbe: "bsfq %rcx, %rax"},
			{ATT: "bsrl", Op: 0xBD, Ext: 0, ATTProbe: "bsrl %ecx, %eax"},
			{ATT: "bsrq", Op: 0xBD, Ext: 1, ATTProbe: "bsrq %rcx, %rax"},
			{ATT: "lzcntl", Prefix: 0xF3, Op: 0xBD, Ext: 0, ATTProbe: "lzcntl %ecx, %eax"},
			{ATT: "lzcntq", Prefix: 0xF3, Op: 0xBD, Ext: 1, ATTProbe: "lzcntq %rcx, %rax"},
			{ATT: "tzcntl", Prefix: 0xF3, Op: 0xBC, Ext: 0, ATTProbe: "tzcntl %ecx, %eax"},
			{ATT: "tzcntq", Prefix: 0xF3, Op: 0xBC, Ext: 1, ATTProbe: "tzcntq %rcx, %rax"},
			{ATT: "popcntl", Prefix: 0xF3, Op: 0xB8, Ext: 0, ATTProbe: "popcntl %ecx, %eax"},
			{ATT: "popcntq", Prefix: 0xF3, Op: 0xB8, Ext: 1, ATTProbe: "popcntq %rcx, %rax"},
		}},
	{Name: "shld", Doc: "the double-precision shifts: 0F Op ib by immediate, 0F Op+1 by cl", FernFn: "x86_gas_shld_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "shld", Suffixed: true, Op: 0xA4, ATTProbe: "shldq %cl, %rdi, %rsi"},
		{ATT: "shrd", Suffixed: true, Op: 0xAC, ATTProbe: "shrdq $5, %rdi, %rsi"},
	}},
	{Name: "bswap", Doc: "byte swap, 32- and 64-bit registers only", Ops: []NamedOp{
		{ATT: "bswap", Suffixed: true, ATTProbe: "bswapq %rax"},
	}},
	{Name: "rmw", Doc: "the read-modify-write exchanges: 0F Op /r for the byte width, 0F Op+1 otherwise", FernFn: "x86_gas_rmw_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "xadd", Suffixed: true, Op: 0xC0, ATTProbe: "xaddq %rcx, %rax"},
		{ATT: "cmpxchg", Suffixed: true, Op: 0xB0, ATTProbe: "cmpxchgq %rcx, (%rdi)"},
	}},
	{Name: "xchg", Doc: "exchange, register or memory on either side", Ops: []NamedOp{
		{ATT: "xchg", Suffixed: true, ATTProbe: "xchgq %rcx, (%rdi)"},
	}},
	{Name: "crc32", Doc: "crc32 with the source width in the suffix", Ops: []NamedOp{
		{ATT: "crc32", Suffixed: true, ATTProbe: "crc32b %cl, %eax"},
	}},

	{Name: "movqd", Doc: "the GPR/xmm moves; direction from which operand is the xmm. AT&T's movq spelling is the mov family's, resolved there", Ops: []NamedOp{
		{ATT: "", ATTProbe: "movq %rax, %xmm0"},
		{ATT: "movd", ATTProbe: "movd %eax, %xmm0"},
	}},
	{Name: "mov10", Doc: "the 0F 10/11 move family; Prefix is the mandatory prefix, 0 for movups", FernFn: "x86_gas_mov10_pfx", Pack: pfxOnly, Ops: []NamedOp{
		{ATT: "movsd", Prefix: 0xF2, ATTProbe: "movsd (%rdi), %xmm0"},
		{ATT: "movss", Prefix: 0xF3, ATTProbe: "movss %xmm2, %xmm1"},
		{ATT: "movups", Prefix: 0x00, ATTProbe: "movups (%rdi), %xmm0"},
		{ATT: "movupd", Prefix: 0x66, ATTProbe: "movupd %xmm0, (%rdi)"},
	}},
	{Name: "movdq", Doc: "the 16-byte moves: 0F 6F load, 0F 7F store, direction from which side is memory", FernFn: "x86_gas_movdq_pfx", Pack: pfxOnly, Ops: []NamedOp{
		{ATT: "movdqu", Prefix: 0xF3, ATTProbe: "movdqu (%rdi), %xmm0"},
		{ATT: "movdqa", Prefix: 0x66, ATTProbe: "movdqa %xmm0, (%rdi)"},
	}},
	{Name: "cvt2s", Doc: "integer to scalar float: Prefix 0F 2A, Ext the REX.W bit the AT&T suffix names; packed as pfx*256 + w",
		FernFn: "x86_gas_cvt2s_op", Pack: pfxExt, Ops: []NamedOp{
			{ATT: "cvtsi2sd", Prefix: 0xF2, Ext: 1, ATTProbe: "cvtsi2sd %rax, %xmm0"},
			{ATT: "cvtsi2sdq", Prefix: 0xF2, Ext: 1, ATTProbe: "cvtsi2sdq %rax, %xmm0"},
			{ATT: "cvtsi2sdl", Prefix: 0xF2, Ext: 0, ATTProbe: "cvtsi2sdl %eax, %xmm0"},
			{ATT: "cvtsi2ss", Prefix: 0xF3, Ext: 1, ATTProbe: "cvtsi2ss %rax, %xmm1"},
			{ATT: "cvtsi2ssq", Prefix: 0xF3, Ext: 1, ATTProbe: "cvtsi2ssq %rax, %xmm1"},
			{ATT: "cvtsi2ssl", Prefix: 0xF3, Ext: 0, ATTProbe: "cvtsi2ssl %eax, %xmm1"},
		}},
	{Name: "cvt2si", Doc: "scalar float to integer: Prefix 0F Op, 2C truncating and 2D rounding; the AT&T l/q suffix names the width the destination register carries; packed as pfx*256 + op",
		FernFn: "x86_gas_cvt2si", Pack: pfxOp, Ops: []NamedOp{
			{ATT: "cvttsd2si", Prefix: 0xF2, Op: 0x2C, ATTProbe: "cvttsd2si %xmm1, %eax"},
			{ATT: "cvttsd2sil", Prefix: 0xF2, Op: 0x2C, ATTProbe: "cvttsd2sil %xmm1, %eax"},
			{ATT: "cvttsd2siq", Prefix: 0xF2, Op: 0x2C, ATTProbe: "cvttsd2siq %xmm1, %rax"},
			{ATT: "cvtsd2si", Prefix: 0xF2, Op: 0x2D, ATTProbe: "cvtsd2si %xmm1, %eax"},
			{ATT: "cvtsd2sil", Prefix: 0xF2, Op: 0x2D, ATTProbe: "cvtsd2sil %xmm1, %eax"},
			{ATT: "cvtsd2siq", Prefix: 0xF2, Op: 0x2D, ATTProbe: "cvtsd2siq %xmm1, %rax"},
			{ATT: "cvttss2si", Prefix: 0xF3, Op: 0x2C, ATTProbe: "cvttss2si %xmm1, %eax"},
			{ATT: "cvttss2sil", Prefix: 0xF3, Op: 0x2C, ATTProbe: "cvttss2sil %xmm1, %eax"},
			{ATT: "cvttss2siq", Prefix: 0xF3, Op: 0x2C, ATTProbe: "cvttss2siq %xmm1, %rax"},
			{ATT: "cvtss2si", Prefix: 0xF3, Op: 0x2D, ATTProbe: "cvtss2si %xmm1, %eax"},
			{ATT: "cvtss2sil", Prefix: 0xF3, Op: 0x2D, ATTProbe: "cvtss2sil %xmm1, %eax"},
			{ATT: "cvtss2siq", Prefix: 0xF3, Op: 0x2D, ATTProbe: "cvtss2siq %xmm1, %rax"},
		}},
	{Name: "imm3a", Doc: "the 66 0F 3A Op /r ib three-operand forms with an xmm destination. pclmulqdq's imm8 picks a 64-bit half of each source, which gas disassembles under a per-value alias name; the encoding is one row", FernFn: "x86_gas_imm3a_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "roundss", Op: 0x0A, ATTProbe: "roundss $0, %xmm1, %xmm0"},
		{ATT: "roundsd", Op: 0x0B, ATTProbe: "roundsd $0, %xmm1, %xmm0"},
		{ATT: "pcmpestri", Op: 0x61, ATTProbe: "pcmpestri $0, %xmm1, %xmm0"},
		{ATT: "pcmpistri", Op: 0x63, ATTProbe: "pcmpistri $0, %xmm1, %xmm0"},
		{ATT: "pclmulqdq", Op: 0x44, ATTProbe: "pclmulqdq $0, %xmm1, %xmm0"},
	}},
	{Name: "shuf", Doc: "the [Prefix] 0F Op /r ib shuffles; packed as pfx*256 + op", FernFn: "x86_gas_shuf_op", Pack: pfxOp, Ops: []NamedOp{
		{ATT: "pshufd", Prefix: 0x66, Op: 0x70, ATTProbe: "pshufd $0, %xmm2, %xmm1"},
		{ATT: "shufps", Prefix: 0x00, Op: 0xC6, ATTProbe: "shufps $0, %xmm2, %xmm1"},
		{ATT: "shufpd", Prefix: 0x66, Op: 0xC6, ATTProbe: "shufpd $1, %xmm2, %xmm1"},
	}},
	{Name: "pextr", Doc: "lane extract into a GPR or memory", Ops: []NamedOp{
		{ATT: "pextrb", ATTProbe: "pextrb $0, %xmm1, %eax"},
		{ATT: "pextrw", ATTProbe: "pextrw $0, %xmm1, %eax"},
		{ATT: "pextrd", ATTProbe: "pextrd $0, %xmm1, %eax"},
		{ATT: "pextrq", ATTProbe: "pextrq $0, %xmm1, %rax"},
	}},
	{Name: "pinsr", Doc: "lane insert from a GPR or memory", Ops: []NamedOp{
		{ATT: "pinsrb", ATTProbe: "pinsrb $0, %eax, %xmm1"},
		{ATT: "pinsrw", ATTProbe: "pinsrw $0, %eax, %xmm1"},
		{ATT: "pinsrd", ATTProbe: "pinsrd $0, %eax, %xmm1"},
		{ATT: "pinsrq", ATTProbe: "pinsrq $0, %rax, %xmm1"},
	}},
	{Name: "movmsk", Doc: "the sign-bit gathers into a GPR: [Prefix] 0F Op /r; packed as pfx*256 + op", FernFn: "x86_gas_movmsk_op", Pack: pfxOp, Ops: []NamedOp{
		{ATT: "pmovmskb", Prefix: 0x66, Op: 0xD7, ATTProbe: "pmovmskb %xmm0, %eax"},
		{ATT: "movmskps", Prefix: 0x00, Op: 0x50, ATTProbe: "movmskps %xmm0, %eax"},
		{ATT: "movmskpd", Prefix: 0x66, Op: 0x50, ATTProbe: "movmskpd %xmm0, %eax"},
	}},
	{Name: "vshift", Doc: "the vector shifts BY IMMEDIATE, the 66 0F Op /Ext ib groups; the by-register spellings are the SSE table's. pslldq/psrldq exist only here; packed as ext*256 + op",
		FernFn: "x86_gas_vshift_op", Pack: extOp, Ops: []NamedOp{
			{ATT: "psrlw", Op: 0x71, Ext: 2, ATTProbe: "psrlw $3, %xmm0"},
			{ATT: "psraw", Op: 0x71, Ext: 4, ATTProbe: "psraw $3, %xmm0"},
			{ATT: "psllw", Op: 0x71, Ext: 6, ATTProbe: "psllw $3, %xmm0"},
			{ATT: "psrld", Op: 0x72, Ext: 2, ATTProbe: "psrld $3, %xmm0"},
			{ATT: "psrad", Op: 0x72, Ext: 4, ATTProbe: "psrad $3, %xmm0"},
			{ATT: "pslld", Op: 0x72, Ext: 6, ATTProbe: "pslld $3, %xmm0"},
			{ATT: "psrlq", Op: 0x73, Ext: 2, ATTProbe: "psrlq $3, %xmm0"},
			{ATT: "psllq", Op: 0x73, Ext: 6, ATTProbe: "psllq $3, %xmm0"},
			{ATT: "psrldq", Op: 0x73, Ext: 3, ATTProbe: "psrldq $8, %xmm0"},
			{ATT: "pslldq", Op: 0x73, Ext: 7, ATTProbe: "pslldq $8, %xmm0"},
		}},
	{Name: "sse38", Doc: "the 66 0F 38 Op /r forms with an xmm destination. SSSE3 and SSE4.1, both well inside the declared x86-64-v3 baseline", FernFn: "x86_gas_sse38_op", Pack: opOnly, Ops: []NamedOp{
		{ATT: "pshufb", Op: 0x00, ATTProbe: "pshufb %xmm1, %xmm0"},
		{ATT: "ptest", Op: 0x17, ATTProbe: "ptest %xmm1, %xmm0"},
		{ATT: "pmulld", Op: 0x40, ATTProbe: "pmulld %xmm1, %xmm0"},
		{ATT: "pminsb", Op: 0x38, ATTProbe: "pminsb %xmm1, %xmm0"},
		{ATT: "pminsd", Op: 0x39, ATTProbe: "pminsd %xmm1, %xmm0"},
		{ATT: "pminuw", Op: 0x3A, ATTProbe: "pminuw %xmm1, %xmm0"},
		{ATT: "pminud", Op: 0x3B, ATTProbe: "pminud %xmm1, %xmm0"},
		{ATT: "pmaxsb", Op: 0x3C, ATTProbe: "pmaxsb %xmm1, %xmm0"},
		{ATT: "pmaxsd", Op: 0x3D, ATTProbe: "pmaxsd %xmm1, %xmm0"},
		{ATT: "pmaxuw", Op: 0x3E, ATTProbe: "pmaxuw %xmm1, %xmm0"},
		{ATT: "pmaxud", Op: 0x3F, ATTProbe: "pmaxud %xmm1, %xmm0"},
	}},
}
