package arm64tbl

import "strings"

// Family is one group of mnemonics both assemblers route through a single
// encoder arm: internal/native/arm64 dispatches on the family name, and
// cmd/arm64tblgen writes an `arm64_gas_is_<Name>` predicate into
// examples/self_host/arm64_native.fern that its dispatch tests instead of
// spelling the mnemonics again. A mnemonic is therefore reachable on both
// sides or on neither.
//
// The encoder logic stays hand-written on each side; what the table holds is
// the vocabulary, the operand shape a representative instruction takes, and —
// where both sides carry a per-mnemonic base word (the scalar FP set, the
// carry chain, the widening multiplies, the negated logicals, the conditional
// selects) — that word, so the two encoders read one constant.
type Family struct {
	Name string
	Doc  string
	// Probe is a representative instruction with %s standing for the
	// mnemonic. It is the test inventory: the native assembler must accept
	// it for every row, and the self-host assembler must encode it to the
	// same word. A row whose shape differs overrides it.
	Probe string
	// Base names the Fern base-word lookup this family generates, "" when
	// the family's encoder computes its word from the mnemonic instead.
	Base string
	Ops  []ScalarOp
}

// ScalarOp is one mnemonic of a family.
type ScalarOp struct {
	Mnemonic string
	// Word is the base encoding when the family has a Base lookup: the
	// instruction with every register field zero. For the shll family it is
	// the packed kind arm64_gas_shll_kind returns.
	Word uint32
	// Probe overrides the family probe for this row.
	Probe string
	// Layout marks a row whose encoding depends on where the image places
	// its sections (adrp), so the two assemblers are compared on acceptance
	// alone rather than on the word.
	Layout bool
}

// ProbeFor is the row's probe with the mnemonic filled in.
func (f Family) ProbeFor(o ScalarOp) string {
	p := o.Probe
	if p == "" {
		p = f.Probe
	}
	return strings.Replace(p, "%s", o.Mnemonic, 1)
}

// Mnemonics is the family's vocabulary in table order.
func (f Family) Mnemonics() []string {
	out := make([]string, 0, len(f.Ops))
	for _, o := range f.Ops {
		out = append(out, o.Mnemonic)
	}
	return out
}

// Scalar is every mnemonic the two arm64 assemblers dispatch by NAME. The
// Advanced SIMD classes in VecTables are dispatched by table lookup on an
// arranged first operand and are not repeated here; the conditional
// branches are matched by pattern (b.<cond> and b<cond>) on both sides.
//
// Every probe is pinned against GNU as through internal/native/arm64, whose
// own tests read their expectations from `aarch64-linux-gnu-as` rather
// than from the manual.
var Scalar = []Family{
	{Name: "fixed", Doc: "no operands", Probe: "%s", Ops: []ScalarOp{{Mnemonic: "ret"}, {Mnemonic: "nop"}}},
	{Name: "sys", Doc: "an imm16 exception class", Probe: "%s #1", Ops: []ScalarOp{{Mnemonic: "svc"}, {Mnemonic: "brk"}}},
	{Name: "indirect", Doc: "branch to a register", Probe: "%s x5", Ops: []ScalarOp{{Mnemonic: "br"}, {Mnemonic: "blr"}}},
	{Name: "branch", Doc: "a label", Probe: "%s l0\nl0:", Ops: []ScalarOp{{Mnemonic: "b"}, {Mnemonic: "bl"}}},
	{Name: "cbranch", Doc: "compare a register against zero and branch", Probe: "%s x0, l0\nl0:",
		Ops: []ScalarOp{{Mnemonic: "cbz"}, {Mnemonic: "cbnz"}}},
	{Name: "tbranch", Doc: "test a bit and branch", Probe: "%s x0, #3, l0\nl0:",
		Ops: []ScalarOp{{Mnemonic: "tbz"}, {Mnemonic: "tbnz"}}},

	{Name: "movwide", Doc: "mov and the move-wide trio", Probe: "%s x1, #7",
		Ops: []ScalarOp{{Mnemonic: "mov", Probe: "mov x0, #42"}, {Mnemonic: "movz"}, {Mnemonic: "movk"}, {Mnemonic: "movn"}}},
	{Name: "symaddr", Doc: "PC-relative address of a symbol", Probe: "%s x0, l0\nl0:",
		Ops: []ScalarOp{
			{Mnemonic: "adrp", Probe: "adrp x0, sym\n.section .rodata\nsym:\n.quad 0", Layout: true},
			{Mnemonic: "adr"},
		}},
	{Name: "addsub", Doc: "add/sub in every operand class, and the cmp/cmn aliases", Probe: "%s x0, x1, x2",
		Ops: []ScalarOp{
			{Mnemonic: "add"}, {Mnemonic: "sub"}, {Mnemonic: "adds"}, {Mnemonic: "subs"},
			{Mnemonic: "cmp", Probe: "cmp x1, x2"}, {Mnemonic: "cmn", Probe: "cmn x1, x2"},
		}},
	{Name: "neg", Doc: "sub from the zero register", Probe: "%s x0, x1",
		Ops: []ScalarOp{{Mnemonic: "neg"}, {Mnemonic: "negs"}}},
	{Name: "logical3", Doc: "the logical ops with an immediate form", Probe: "%s x0, x1, x2",
		Ops: []ScalarOp{{Mnemonic: "and"}, {Mnemonic: "orr"}, {Mnemonic: "eor"}}},
	{Name: "logical2", Doc: "flag-setting and negated logicals, with the tst/mvn aliases",
		Probe: "%s x0, x1, x2", Base: "arm64_logical2_base",
		Ops: []ScalarOp{
			{Mnemonic: "ands", Word: 0xEA000000}, {Mnemonic: "bic", Word: 0x8A200000},
			{Mnemonic: "bics", Word: 0xEA200000}, {Mnemonic: "orn", Word: 0xAA200000},
			{Mnemonic: "eon", Word: 0xCA200000},
			{Mnemonic: "tst", Word: 0xEA000000, Probe: "tst x0, x1"},
			{Mnemonic: "mvn", Word: 0xAA200000, Probe: "mvn x0, x1"},
		}},
	{Name: "carry", Doc: "add/sub with carry; ngc/ngcs are the Rn=XZR aliases",
		Probe: "%s x0, x1, x2", Base: "arm64_carry_base",
		Ops: []ScalarOp{
			{Mnemonic: "adc", Word: 0x9A000000}, {Mnemonic: "adcs", Word: 0xBA000000},
			{Mnemonic: "sbc", Word: 0xDA000000}, {Mnemonic: "sbcs", Word: 0xFA000000},
			{Mnemonic: "ngc", Word: 0xDA000000, Probe: "ngc x0, x1"},
			{Mnemonic: "ngcs", Word: 0xFA000000, Probe: "ngcs x0, x1"},
		}},
	{Name: "mulwide", Doc: "the high-half and widening multiplies, and madd",
		Probe: "%s x0, x1, x2", Base: "arm64_mulwide_base",
		Ops: []ScalarOp{
			{Mnemonic: "umulh", Word: 0x9BC07C00}, {Mnemonic: "smulh", Word: 0x9B407C00},
			{Mnemonic: "madd", Word: 0x9B000000, Probe: "madd x0, x1, x2, x3"},
			{Mnemonic: "smull", Word: 0x9B200000, Probe: "smull x0, w1, w2"},
			{Mnemonic: "umull", Word: 0x9BA00000, Probe: "umull x0, w1, w2"},
			{Mnemonic: "smaddl", Word: 0x9B200000, Probe: "smaddl x0, w1, w2, x3"},
			{Mnemonic: "umaddl", Word: 0x9BA00000, Probe: "umaddl x0, w1, w2, x3"},
			{Mnemonic: "smsubl", Word: 0x9B208000, Probe: "smsubl x0, w1, w2, x3"},
			{Mnemonic: "umsubl", Word: 0x9BA08000, Probe: "umsubl x0, w1, w2, x3"},
		}},
	{Name: "muldiv", Doc: "mul, the divides, and msub", Probe: "%s x0, x1, x2",
		Ops: []ScalarOp{{Mnemonic: "mul"}, {Mnemonic: "udiv"}, {Mnemonic: "sdiv"},
			{Mnemonic: "msub", Probe: "msub x0, x1, x2, x3"}}},
	{Name: "bitfield", Doc: "the BFM-family insert aliases", Probe: "%s x0, x1, #4, #8",
		Ops: []ScalarOp{{Mnemonic: "bfi"}, {Mnemonic: "bfxil"}, {Mnemonic: "ubfiz"}, {Mnemonic: "sbfiz"}}},
	{Name: "bfx", Doc: "the BFM-family extract aliases", Probe: "%s x0, x1, #4, #8",
		Ops: []ScalarOp{{Mnemonic: "ubfx"}, {Mnemonic: "sbfx"}}},
	{Name: "extr_ror", Doc: "extract, and ror as its Rn=Rm alias or as RORV", Probe: "%s x0, x1, x2, #12",
		Ops: []ScalarOp{{Mnemonic: "extr"}, {Mnemonic: "ror", Probe: "ror x0, x1, #7"}}},
	{Name: "condsel", Doc: "conditional select and compare, with the inverting aliases",
		Probe: "%s x0, x1, x2, lt", Base: "arm64_condsel_base",
		Ops: []ScalarOp{
			{Mnemonic: "csel", Word: 0x9A800000}, {Mnemonic: "csinc", Word: 0x9A800400},
			{Mnemonic: "csinv", Word: 0xDA800000}, {Mnemonic: "csneg", Word: 0xDA800400},
			{Mnemonic: "cset", Word: 0x9A800400, Probe: "cset x0, ne"},
			{Mnemonic: "csetm", Word: 0xDA800000, Probe: "csetm x0, lt"},
			{Mnemonic: "cinc", Word: 0x9A800400, Probe: "cinc x0, x1, lt"},
			{Mnemonic: "cinv", Word: 0xDA800000, Probe: "cinv x0, x1, lt"},
			{Mnemonic: "cneg", Word: 0xDA800400, Probe: "cneg x0, x1, lt"},
			{Mnemonic: "ccmp", Word: 0xFA400000, Probe: "ccmp x0, x1, #0, eq"},
			{Mnemonic: "ccmn", Word: 0xBA400000, Probe: "ccmn x0, #9, #15, lt"},
		}},
	{Name: "bitops", Doc: "the one-source data-processing ops", Probe: "%s x0, x1",
		Ops: []ScalarOp{{Mnemonic: "clz"}, {Mnemonic: "cls"}, {Mnemonic: "rbit"},
			{Mnemonic: "rev"}, {Mnemonic: "rev16"}, {Mnemonic: "rev32"}}},
	{Name: "shift", Doc: "shifts by immediate (BFM aliases) or by register", Probe: "%s x0, x1, #4",
		Ops: []ScalarOp{{Mnemonic: "lsl"}, {Mnemonic: "lsr"}, {Mnemonic: "asr"}}},
	{Name: "extend", Doc: "the sign/zero extends (BFM aliases)", Probe: "%s x0, w1",
		Ops: []ScalarOp{{Mnemonic: "sxtb"}, {Mnemonic: "sxth"}, {Mnemonic: "sxtw"},
			{Mnemonic: "uxtb", Probe: "uxtb w0, w1"}, {Mnemonic: "uxth", Probe: "uxth w0, w1"}}},

	{Name: "ldst", Doc: "the 32/64-bit and SIMD&FP single-register loads and stores", Probe: "%s x0, [x1, #16]",
		Ops: []ScalarOp{{Mnemonic: "ldr"}, {Mnemonic: "str"}}},
	{Name: "ldst_narrow", Doc: "the byte and halfword accesses, and the sign-extending loads", Probe: "%s w0, [x1, #2]",
		Ops: []ScalarOp{{Mnemonic: "ldrb"}, {Mnemonic: "strb"}, {Mnemonic: "ldrh"}, {Mnemonic: "strh"},
			{Mnemonic: "ldrsb", Probe: "ldrsb x0, [x1, #2]"}, {Mnemonic: "ldrsh", Probe: "ldrsh x0, [x1, #2]"},
			{Mnemonic: "ldrsw", Probe: "ldrsw x0, [x1, #4]"}}},
	{Name: "unscaled", Doc: "the explicit unscaled-offset 32/64-bit and SIMD&FP forms", Probe: "%s x0, [x1, #-8]",
		Ops: []ScalarOp{{Mnemonic: "ldur"}, {Mnemonic: "stur"}}},
	{Name: "unscaled2", Doc: "unscaled byte/halfword accesses and sign-extending loads", Probe: "%s w0, [x1, #-1]",
		Ops: []ScalarOp{{Mnemonic: "ldurb"}, {Mnemonic: "sturb"}, {Mnemonic: "ldurh"}, {Mnemonic: "sturh"},
			{Mnemonic: "ldursb", Probe: "ldursb x0, [x1, #-1]"}, {Mnemonic: "ldursh", Probe: "ldursh x0, [x1, #-2]"},
			{Mnemonic: "ldursw", Probe: "ldursw x0, [x1, #-4]"}}},
	{Name: "pair", Doc: "register pairs in the three addressing modes", Probe: "%s x0, x1, [x2, #16]",
		Ops: []ScalarOp{{Mnemonic: "stp"}, {Mnemonic: "ldp"}}},
	{Name: "excl_ld", Doc: "load-exclusive, plain and acquire", Probe: "%s x0, [x1]",
		Ops: []ScalarOp{{Mnemonic: "ldxr"}, {Mnemonic: "ldaxr"},
			{Mnemonic: "ldxrb", Probe: "ldxrb w0, [x1]"}, {Mnemonic: "ldaxrb", Probe: "ldaxrb w0, [x1]"},
			{Mnemonic: "ldxrh", Probe: "ldxrh w0, [x1]"}, {Mnemonic: "ldaxrh", Probe: "ldaxrh w0, [x1]"}}},
	{Name: "excl_st", Doc: "store-exclusive, plain and release", Probe: "%s w2, x0, [x1]",
		Ops: []ScalarOp{{Mnemonic: "stxr"}, {Mnemonic: "stlxr"},
			{Mnemonic: "stxrb", Probe: "stxrb w2, w0, [x1]"}, {Mnemonic: "stlxrb", Probe: "stlxrb w2, w0, [x1]"},
			{Mnemonic: "stxrh", Probe: "stxrh w2, w0, [x1]"}, {Mnemonic: "stlxrh", Probe: "stlxrh w2, w0, [x1]"}}},
	{Name: "acqrel", Doc: "load-acquire and store-release", Probe: "%s x0, [x1]",
		Ops: []ScalarOp{{Mnemonic: "ldar"}, {Mnemonic: "stlr"},
			{Mnemonic: "ldarb", Probe: "ldarb w0, [x1]"}, {Mnemonic: "ldarh", Probe: "ldarh w0, [x1]"},
			{Mnemonic: "stlrb", Probe: "stlrb w0, [x1]"}, {Mnemonic: "stlrh", Probe: "stlrh w0, [x1]"}}},
	{Name: "barrier", Doc: "the memory and instruction barriers", Probe: "%s ish",
		Ops: []ScalarOp{{Mnemonic: "dmb"}, {Mnemonic: "dsb"}, {Mnemonic: "isb", Probe: "isb"}}},
	{Name: "sysreg", Doc: "system-register reads and writes", Probe: "%s x9, cntvct_el0",
		Ops: []ScalarOp{{Mnemonic: "mrs"}, {Mnemonic: "msr", Probe: "msr tpidr_el0, x9"}}},

	{Name: "fp3", Doc: "three-register scalar FP; the word is the double-precision form, single clears bit 22",
		Probe: "%s d0, d1, d2", Base: "arm64_fp3_base",
		Ops: []ScalarOp{
			{Mnemonic: "fadd", Word: 0x1E602800}, {Mnemonic: "fsub", Word: 0x1E603800},
			{Mnemonic: "fmul", Word: 0x1E600800}, {Mnemonic: "fdiv", Word: 0x1E601800},
			{Mnemonic: "fnmul", Word: 0x1E608800}, {Mnemonic: "fmin", Word: 0x1E605800},
			{Mnemonic: "fmax", Word: 0x1E604800}, {Mnemonic: "fminnm", Word: 0x1E607800},
			{Mnemonic: "fmaxnm", Word: 0x1E606800},
		}},
	{Name: "fp4", Doc: "the fused multiply-adds", Probe: "%s d0, d1, d2, d3", Base: "arm64_fp4_base",
		Ops: []ScalarOp{
			{Mnemonic: "fmadd", Word: 0x1F400000}, {Mnemonic: "fmsub", Word: 0x1F408000},
			{Mnemonic: "fnmadd", Word: 0x1F600000}, {Mnemonic: "fnmsub", Word: 0x1F608000},
		}},
	{Name: "fpcond", Doc: "conditional FP select and compare", Probe: "fcsel d0, d1, d2, lt",
		Ops: []ScalarOp{{Mnemonic: "fcsel"}, {Mnemonic: "fccmp", Probe: "fccmp d0, d1, #15, lt"}}},
	{Name: "fcmp", Doc: "FP compare, quiet and signalling", Probe: "%s d1, d2",
		Ops: []ScalarOp{{Mnemonic: "fcmp"}, {Mnemonic: "fcmpe"}}},
	{Name: "funary", Doc: "unary scalar FP; the word is the double-precision form", Probe: "%s d0, d1",
		Base: "arm64_funary_d_base",
		Ops: []ScalarOp{
			{Mnemonic: "fneg", Word: 0x1E614000}, {Mnemonic: "fabs", Word: 0x1E60C000},
			{Mnemonic: "fsqrt", Word: 0x1E61C000}, {Mnemonic: "frintm", Word: 0x1E654000},
			{Mnemonic: "frintp", Word: 0x1E64C000}, {Mnemonic: "frintz", Word: 0x1E65C000},
			{Mnemonic: "frinta", Word: 0x1E664000}, {Mnemonic: "frintn", Word: 0x1E644000},
		}},
	{Name: "fcvt", Doc: "precision conversion", Probe: "fcvt d0, s1", Ops: []ScalarOp{{Mnemonic: "fcvt"}}},
	{Name: "fcvtz", Doc: "FP to integer, toward zero", Probe: "%s x0, d1",
		Ops: []ScalarOp{{Mnemonic: "fcvtzs"}, {Mnemonic: "fcvtzu"}}},
	{Name: "cvtf", Doc: "integer to FP", Probe: "%s d0, x1",
		Ops: []ScalarOp{{Mnemonic: "scvtf"}, {Mnemonic: "ucvtf"}}},
	{Name: "fmov", Doc: "FP moves between register files and the FP immediate", Probe: "fmov d0, d1",
		Ops: []ScalarOp{{Mnemonic: "fmov"}}},

	{Name: "vecmov", Doc: "lane moves between the vector and general register files", Probe: "%s w0, v1.b[5]",
		Ops: []ScalarOp{{Mnemonic: "umov"}, {Mnemonic: "smov"},
			{Mnemonic: "ins", Probe: "ins v0.b[5], w1"}, {Mnemonic: "dup", Probe: "dup v0.16b, w1"}}},
	{Name: "movi", Doc: "the vector modified immediate", Probe: "movi v0.16b, #7", Ops: []ScalarOp{{Mnemonic: "movi"}}},
	{Name: "vecldst", Doc: "single-structure vector loads and stores", Probe: "%s {v1.16b}, [x0]",
		Ops: []ScalarOp{{Mnemonic: "ld1"}, {Mnemonic: "st1"}, {Mnemonic: "ld1r"}}},
	{Name: "vecext", Doc: "byte extract and table lookup", Probe: "ext v0.16b, v1.16b, v2.16b, #5",
		Ops: []ScalarOp{{Mnemonic: "ext"}, {Mnemonic: "tbl", Probe: "tbl v0.16b, {v1.16b}, v2.16b"}}},
	{Name: "narrow", Doc: "the narrowing moves and shifts", Probe: "%s v0.8b, v1.8h",
		Ops: []ScalarOp{{Mnemonic: "xtn"}, {Mnemonic: "xtn2", Probe: "xtn2 v0.16b, v1.8h"},
			{Mnemonic: "shrn", Probe: "shrn v1.8b, v1.8h, #4"}, {Mnemonic: "shrn2", Probe: "shrn2 v0.16b, v1.8h, #3"}}},
	{Name: "shll", Doc: "the widening shifts; the word is the packed kind: bit 0 unsigned, bit 1 the `2` suffix, bit 2 an sxtl/uxtl alias",
		Probe: "%s v0.8h, v1.8b, #3", Base: "arm64_gas_shll_kind",
		Ops: []ScalarOp{
			{Mnemonic: "sshll", Word: 0}, {Mnemonic: "sshll2", Word: 2, Probe: "sshll2 v0.8h, v1.16b, #3"},
			{Mnemonic: "ushll", Word: 1}, {Mnemonic: "ushll2", Word: 3, Probe: "ushll2 v0.2d, v1.4s, #17"},
			{Mnemonic: "sxtl", Word: 4, Probe: "sxtl v0.8h, v1.8b"}, {Mnemonic: "sxtl2", Word: 6, Probe: "sxtl2 v0.4s, v1.8h"},
			{Mnemonic: "uxtl", Word: 5, Probe: "uxtl v0.2d, v1.2s"}, {Mnemonic: "uxtl2", Word: 7, Probe: "uxtl2 v0.8h, v1.16b"},
		}},
}

// scalarIndex maps a mnemonic to its family and row.
var scalarIndex = func() map[string][2]int {
	m := make(map[string][2]int, 256)
	for fi, f := range Scalar {
		for oi, o := range f.Ops {
			if _, dup := m[o.Mnemonic]; dup {
				panic("arm64tbl: " + o.Mnemonic + " listed twice")
			}
			m[o.Mnemonic] = [2]int{fi, oi}
		}
	}
	return m
}()

// FamilyOf looks a mnemonic up. ok is false for a spelling no family lists.
func FamilyOf(mnem string) (fam *Family, op *ScalarOp, ok bool) {
	idx, found := scalarIndex[mnem]
	if !found {
		return nil, nil, false
	}
	return &Scalar[idx[0]], &Scalar[idx[0]].Ops[idx[1]], true
}

// ScalarMnemonics is the whole by-name vocabulary in table order.
func ScalarMnemonics() []string {
	var out []string
	for _, f := range Scalar {
		out = append(out, f.Mnemonics()...)
	}
	return out
}
