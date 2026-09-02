package x86tbl

// SSEHalf says which of the self-host assembler's two lookup functions
// carries a row. The split is not cosmetic: x86_gas_emit consults the float
// half before the integer one, so a row in the wrong half is reachable only
// if no earlier arm claims the mnemonic first.
type SSEHalf int

const (
	// SSEFloatHalf is x86_gas_sse_fp_op — the scalar and packed FLOAT forms.
	SSEFloatHalf SSEHalf = iota
	// SSEIntHalf is x86_gas_sse_int_op — the packed-INTEGER forms, all
	// 66-prefixed.
	SSEIntHalf
	// SSENoHalf marks a row both assemblers keep OUT of their tables.
	// movdqa / movdqu are the only ones: AT&T decides store-versus-load from
	// which operand is the xmm, so each side routes them to a dedicated
	// encoder (x86_gas_movdq, and the direction check in asm.go's insn)
	// rather than a table lookup that assumes a direction.
	SSENoHalf
)

// SSEOp is one two-operand `[prefix] 0F op /r` form with an xmm destination.
//
// Prefix 0 means none — the packed-single forms take no mandatory prefix, and
// the self-host writes those as a bare opcode rather than `0 * 256 + op`.
type SSEOp struct {
	Mnemonic string
	Prefix   byte
	Op       byte
	Half     SSEHalf
}

// SSEOps is the two-byte-opcode SSE vocabulary, in the order the self-host
// dispatch reads it — which is why this is a slice and not a map. Generating
// the Fern side from a Go map would reorder it on every run.
var SSEOps = []SSEOp{
	{"addsd", 0xF2, 0x58, SSEFloatHalf},
	{"subsd", 0xF2, 0x5C, SSEFloatHalf},
	{"mulsd", 0xF2, 0x59, SSEFloatHalf},
	{"divsd", 0xF2, 0x5E, SSEFloatHalf},
	{"sqrtsd", 0xF2, 0x51, SSEFloatHalf},
	{"minsd", 0xF2, 0x5D, SSEFloatHalf},
	{"maxsd", 0xF2, 0x5F, SSEFloatHalf},
	{"addss", 0xF3, 0x58, SSEFloatHalf},
	{"subss", 0xF3, 0x5C, SSEFloatHalf},
	{"mulss", 0xF3, 0x59, SSEFloatHalf},
	{"divss", 0xF3, 0x5E, SSEFloatHalf},
	{"sqrtss", 0xF3, 0x51, SSEFloatHalf},
	{"minss", 0xF3, 0x5D, SSEFloatHalf},
	{"maxss", 0xF3, 0x5F, SSEFloatHalf},
	{"ucomisd", 0x66, 0x2E, SSEFloatHalf},
	{"comisd", 0x66, 0x2F, SSEFloatHalf},
	{"ucomiss", 0x00, 0x2E, SSEFloatHalf},
	{"comiss", 0x00, 0x2F, SSEFloatHalf},
	{"cvtss2sd", 0xF3, 0x5A, SSEFloatHalf},
	{"cvtsd2ss", 0xF2, 0x5A, SSEFloatHalf},
	{"movapd", 0x66, 0x28, SSEFloatHalf},
	{"movaps", 0x00, 0x28, SSEFloatHalf},
	{"xorpd", 0x66, 0x57, SSEFloatHalf},
	{"xorps", 0x00, 0x57, SSEFloatHalf},
	{"andpd", 0x66, 0x54, SSEFloatHalf},
	{"andps", 0x00, 0x54, SSEFloatHalf},
	{"andnpd", 0x66, 0x55, SSEFloatHalf},
	{"andnps", 0x00, 0x55, SSEFloatHalf},
	{"orpd", 0x66, 0x56, SSEFloatHalf},
	{"orps", 0x00, 0x56, SSEFloatHalf},
	{"addpd", 0x66, 0x58, SSEFloatHalf},
	{"subpd", 0x66, 0x5C, SSEFloatHalf},
	{"mulpd", 0x66, 0x59, SSEFloatHalf},
	{"divpd", 0x66, 0x5E, SSEFloatHalf},
	{"sqrtpd", 0x66, 0x51, SSEFloatHalf},
	{"minpd", 0x66, 0x5D, SSEFloatHalf},
	{"maxpd", 0x66, 0x5F, SSEFloatHalf},
	{"addps", 0x00, 0x58, SSEFloatHalf},
	{"subps", 0x00, 0x5C, SSEFloatHalf},
	{"mulps", 0x00, 0x59, SSEFloatHalf},
	{"divps", 0x00, 0x5E, SSEFloatHalf},
	{"sqrtps", 0x00, 0x51, SSEFloatHalf},
	{"minps", 0x00, 0x5D, SSEFloatHalf},
	{"maxps", 0x00, 0x5F, SSEFloatHalf},
	{"unpcklpd", 0x66, 0x14, SSEFloatHalf},
	{"unpckhpd", 0x66, 0x15, SSEFloatHalf},
	{"cvtdq2ps", 0x00, 0x5B, SSEFloatHalf},
	{"cvtps2dq", 0x66, 0x5B, SSEFloatHalf},
	{"cvttps2dq", 0xF3, 0x5B, SSEFloatHalf},
	{"cvtdq2pd", 0xF3, 0xE6, SSEFloatHalf},
	{"cvtpd2dq", 0xF2, 0xE6, SSEFloatHalf},
	{"cvttpd2dq", 0x66, 0xE6, SSEFloatHalf},
	{"pcmpeqb", 0x66, 0x74, SSEIntHalf},
	{"pcmpeqw", 0x66, 0x75, SSEIntHalf},
	{"pcmpeqd", 0x66, 0x76, SSEIntHalf},
	{"pcmpgtb", 0x66, 0x64, SSEIntHalf},
	{"pcmpgtw", 0x66, 0x65, SSEIntHalf},
	{"pcmpgtd", 0x66, 0x66, SSEIntHalf},
	{"punpcklbw", 0x66, 0x60, SSEIntHalf},
	{"punpcklwd", 0x66, 0x61, SSEIntHalf},
	{"punpckldq", 0x66, 0x62, SSEIntHalf},
	{"punpcklqdq", 0x66, 0x6C, SSEIntHalf},
	{"punpckhbw", 0x66, 0x68, SSEIntHalf},
	{"punpckhwd", 0x66, 0x69, SSEIntHalf},
	{"punpckhdq", 0x66, 0x6A, SSEIntHalf},
	{"punpckhqdq", 0x66, 0x6D, SSEIntHalf},
	{"por", 0x66, 0xEB, SSEIntHalf},
	{"pand", 0x66, 0xDB, SSEIntHalf},
	{"pxor", 0x66, 0xEF, SSEIntHalf},
	{"pandn", 0x66, 0xDF, SSEIntHalf},
	{"paddb", 0x66, 0xFC, SSEIntHalf},
	{"paddw", 0x66, 0xFD, SSEIntHalf},
	{"paddd", 0x66, 0xFE, SSEIntHalf},
	{"paddq", 0x66, 0xD4, SSEIntHalf},
	{"psubb", 0x66, 0xF8, SSEIntHalf},
	{"psubw", 0x66, 0xF9, SSEIntHalf},
	{"psubd", 0x66, 0xFA, SSEIntHalf},
	{"psubq", 0x66, 0xFB, SSEIntHalf},
	{"paddusb", 0x66, 0xDC, SSEIntHalf},
	{"psubusb", 0x66, 0xD8, SSEIntHalf},
	{"paddsb", 0x66, 0xEC, SSEIntHalf},
	{"psubsb", 0x66, 0xE8, SSEIntHalf},
	{"pavgb", 0x66, 0xE0, SSEIntHalf},
	{"pminub", 0x66, 0xDA, SSEIntHalf},
	{"pmaxub", 0x66, 0xDE, SSEIntHalf},
	{"pminsw", 0x66, 0xEA, SSEIntHalf},
	{"pmaxsw", 0x66, 0xEE, SSEIntHalf},
	{"pmullw", 0x66, 0xD5, SSEIntHalf},
	{"pmulhw", 0x66, 0xE5, SSEIntHalf},
	{"pmulhuw", 0x66, 0xE4, SSEIntHalf},
	{"pmuludq", 0x66, 0xF4, SSEIntHalf},
	{"psadbw", 0x66, 0xF6, SSEIntHalf},
	{"packsswb", 0x66, 0x63, SSEIntHalf},
	{"packuswb", 0x66, 0x67, SSEIntHalf},
	{"packssdw", 0x66, 0x6B, SSEIntHalf},
	{"psllw", 0x66, 0xF1, SSEIntHalf},
	{"pslld", 0x66, 0xF2, SSEIntHalf},
	{"psllq", 0x66, 0xF3, SSEIntHalf},
	{"psrlw", 0x66, 0xD1, SSEIntHalf},
	{"psrld", 0x66, 0xD2, SSEIntHalf},
	{"psrlq", 0x66, 0xD3, SSEIntHalf},
	{"psraw", 0x66, 0xE1, SSEIntHalf},
	{"psrad", 0x66, 0xE2, SSEIntHalf},
	// Not in either half — see SSENoHalf.
	{"movdqu", 0xF3, 0x6F, SSENoHalf},
	{"movdqa", 0x66, 0x6F, SSENoHalf},
}

// SSEOpMap is the flat lookup the Go assembler dispatches on, movdqa/movdqu
// included: that pair leaves the self-host's TABLES, not the vocabulary, and
// the Go side still resolves them here after its own direction check.
func SSEOpMap() map[string]struct{ Prefix, Op byte } {
	m := make(map[string]struct{ Prefix, Op byte }, len(SSEOps))
	for _, o := range SSEOps {
		m[o.Mnemonic] = struct{ Prefix, Op byte }{o.Prefix, o.Op}
	}
	return m
}

// SSEHalfOps is the rows of one half, in dispatch order.
func SSEHalfOps(h SSEHalf) []SSEOp {
	var out []SSEOp
	for _, o := range SSEOps {
		if o.Half == h {
			out = append(out, o)
		}
	}
	return out
}
