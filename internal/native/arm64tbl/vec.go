// Package arm64tbl is the single source of truth for the arm64 encoding
// tables both assemblers need (#7903) — the arm64 twin of x86tbl.
//
// The Advanced SIMD classes are the tabular part of the arm64 vocabulary:
// per mnemonic a U bit, an opcode, and one class-specific extra (the element
// sizes the encoding has, an szHi bit, a shift direction, a widening flag).
// internal/native/arm64 reads these directly and cmd/arm64tblgen writes the
// self-host lookups in examples/self_host/arm64_native.fern from them, so a
// mnemonic one side knows and the other does not cannot exist.
//
// Every row is pinned against GNU as: the size sets, and every size
// deliberately absent (mul/min/max have no .2d, the bitwise ops and cnt are
// byte-only), are what `aarch64-linux-gnu-as` accepts. A mask that is too
// wide encodes a different instruction rather than failing.
package arm64tbl

// Element-size masks: which arrangements an op's encoding has.
const (
	ArrB    = 1 << 0
	ArrH    = 1 << 1
	ArrS    = 1 << 2
	ArrD    = 1 << 3
	ArrBHS  = ArrB | ArrH | ArrS
	ArrBHSD = ArrBHS | ArrD
)

// VecOp is one row: the mnemonic, its U bit, its opcode, and the
// class-specific extra field the table's Aux documents.
type VecOp struct {
	Mnemonic string
	U        bool
	Opcode   uint32
	Aux      uint32
}

// VecTable is one Advanced SIMD class. Ops are in the order the self-host
// lookup is written in — a slice rather than a map, so the generated Fern
// does not reorder between runs.
type VecTable struct {
	// FernFn is the self-host lookup this table generates.
	FernFn string
	// Aux says what Ops[i].Aux carries, for the generated comment.
	Aux string
	// Doc is the comment above the generated function.
	Doc string
	Ops []VecOp
}

// VecInt3 is the three-register integer class. Aux is the size mask.
var VecInt3 = VecTable{
	FernFn: "arm64_v3int_entry",
	Aux:    "sizes",
	Doc:    "// Three-register integer ops. 15 = BHSD, 7 = BHS.\n",
	Ops: []VecOp{
		{"add", false, 0x10, ArrBHSD}, {"sub", true, 0x10, ArrBHSD}, {"mul", false, 0x13, ArrBHS},
		{"cmeq", true, 0x11, ArrBHSD}, {"cmtst", false, 0x11, ArrBHSD},
		{"cmgt", false, 0x06, ArrBHSD}, {"cmge", false, 0x07, ArrBHSD},
		{"cmhi", true, 0x06, ArrBHSD}, {"cmhs", true, 0x07, ArrBHSD},
		{"smax", false, 0x0C, ArrBHS}, {"smin", false, 0x0D, ArrBHS},
		{"umax", true, 0x0C, ArrBHS}, {"umin", true, 0x0D, ArrBHS},
		{"sshl", false, 0x08, ArrBHSD}, {"ushl", true, 0x08, ArrBHSD},
	},
}

// VecLogical3 is the bitwise three-register class, which puts the
// operation in the size field and so exists only in the byte arrangements.
// Opcode is that operation selector; Aux is the fixed byte-only mask.
var VecLogical3 = VecTable{
	FernFn: "arm64_vlogical_entry",
	Aux:    "sizes",
	Doc: "// The bitwise three-register ops put the operation in the size field, so they\n" +
		"// exist only in the byte arrangements. The entry carries that operation\n" +
		"// selector in the opcode slot; the real opcode is always 3.\n",
	Ops: []VecOp{
		{"and", false, 0, ArrB}, {"bic", false, 1, ArrB}, {"orr", false, 2, ArrB},
		{"orn", false, 3, ArrB}, {"eor", true, 0, ArrB},
	},
}

// VecCmpZero is compare-against-zero (two-register misc; #0 is part of the
// opcode). Aux is the size mask.
var VecCmpZero = VecTable{
	FernFn: "arm64_vcmpzero_entry",
	Aux:    "sizes",
	Doc: "// Compare against zero. cmle/cmlt exist ONLY in this form — their\n" +
		"// register-operand spellings are the swapped-operand cmge/cmgt.\n",
	Ops: []VecOp{
		{"cmeq", false, 0x09, ArrBHSD}, {"cmgt", false, 0x08, ArrBHSD}, {"cmge", true, 0x08, ArrBHSD},
		{"cmle", true, 0x09, ArrBHSD}, {"cmlt", false, 0x0A, ArrBHSD},
	},
}

// VecInt2Misc is the two-register unary integer class. Aux is the size mask.
var VecInt2Misc = VecTable{
	FernFn: "arm64_v2misc_entry",
	Aux:    "sizes",
	Doc:    "// Two-register unary integer ops. mvn is an alias of not.\n",
	Ops: []VecOp{
		{"neg", true, 0x0B, ArrBHSD}, {"abs", false, 0x0B, ArrBHSD},
		{"not", true, 0x05, ArrB}, {"mvn", true, 0x05, ArrB}, {"cnt", false, 0x05, ArrB},
		{"rev16", false, 0x01, ArrB}, {"rev32", true, 0x00, ArrB | ArrH}, {"rev64", false, 0x00, ArrBHS},
	},
}

// VecFP3 is the lane-wise FP three-register class. Aux is the szHi bit: the
// encoding reads size as szHi<<1 | (D lanes), so the tables carry szHi where
// the integer ones carry a mask; the arrangements are 2s/4s/2d throughout.
var VecFP3 = VecTable{
	FernFn: "arm64_vfp3_entry",
	Aux:    "szHi",
	Doc: "// arm64_vfp3_entry: three-register lane-wise FP. The szHi bit rides in the\n" +
		"// size-mask slot, which is free here because the arrangement set is fixed.\n",
	Ops: []VecOp{
		{"fadd", false, 0x1A, 0}, {"fsub", false, 0x1A, 1}, {"fmul", true, 0x1B, 0},
		{"fdiv", true, 0x1F, 0}, {"fmax", false, 0x1E, 0}, {"fmin", false, 0x1E, 1},
		{"fcmeq", false, 0x1C, 0}, {"fcmge", true, 0x1C, 0}, {"fcmgt", true, 0x1C, 1},
	},
}

// VecFP2Misc is the lane-wise FP two-register class. Aux is szHi.
var VecFP2Misc = VecTable{
	FernFn: "arm64_vfp2_entry",
	Aux:    "szHi",
	Doc:    "// arm64_vfp2_entry: two-register lane-wise FP, same szHi convention.\n",
	Ops: []VecOp{
		{"fneg", true, 0x0F, 1}, {"fabs", false, 0x0F, 1}, {"fsqrt", true, 0x1F, 1},
		{"scvtf", false, 0x1D, 0}, {"ucvtf", true, 0x1D, 0},
		{"fcvtzs", false, 0x1B, 1}, {"fcvtzu", true, 0x1B, 1},
	},
}

// VecFPCmpZero is FP compare against zero (operand spelled #0.0). szHi is
// always set for the class, so Aux is unused (0).
var VecFPCmpZero = VecTable{
	FernFn: "arm64_vfpcmpzero_entry",
	Aux:    "unused",
	Doc: "// FP compare against zero, whose operand is spelled `#0.0`. szHi is always\n" +
		"// set for this class, so the entry needs no flag. fcmle/fcmlt exist ONLY\n" +
		"// here — their register-operand spellings are the swapped-operand\n" +
		"// fcmge/fcmgt.\n",
	Ops: []VecOp{
		{"fcmeq", false, 0x0D, 0}, {"fcmgt", false, 0x0C, 0}, {"fcmge", true, 0x0C, 0},
		{"fcmle", true, 0x0D, 0}, {"fcmlt", false, 0x0E, 0},
	},
}

// VecShiftImm is shift-by-immediate. Aux is the direction: 1 = left.
var VecShiftImm = VecTable{
	FernFn: "arm64_vshift_entry",
	Aux:    "left",
	Doc:    "// Shift by immediate. The size-mask slot carries the direction: 1 = left.\n",
	Ops: []VecOp{
		{"shl", false, 0x0A, 1}, {"sli", true, 0x0A, 1},
		{"sshr", false, 0x00, 0}, {"ushr", true, 0x00, 0}, {"sri", true, 0x08, 0},
	},
}

// VecPermute is zip/uzp/trn. Opcode is the 3-bit opc; U and Aux unused.
var VecPermute = VecTable{
	FernFn: "arm64_vpermute_opc",
	Aux:    "unused",
	Doc:    "",
	Ops: []VecOp{
		{"zip1", false, 3, 0}, {"zip2", false, 7, 0}, {"uzp1", false, 1, 0},
		{"uzp2", false, 5, 0}, {"trn1", false, 2, 0}, {"trn2", false, 6, 0},
	},
}

// VecAcross is the across-lanes reductions. Aux is the widening flag:
// saddlv/uaddlv produce a result one element size up.
var VecAcross = VecTable{
	FernFn: "arm64_across_entry",
	Aux:    "widen",
	Doc: "// arm64_across_entry: the across-lanes table. The size-mask slot carries the\n" +
		"// WIDENING flag — saddlv/uaddlv produce a result one element size up, so\n" +
		"// their scalar destination is a class wider than the arrangement.\n",
	Ops: []VecOp{
		{"addv", false, 0x1B, 0}, {"smaxv", false, 0x0A, 0}, {"sminv", false, 0x1A, 0},
		{"umaxv", true, 0x0A, 0}, {"uminv", true, 0x1A, 0},
		{"saddlv", false, 0x03, 1}, {"uaddlv", true, 0x03, 1},
	},
}

// VecTables is every class, for the gates that enumerate the vocabulary.
var VecTables = []VecTable{VecInt3, VecLogical3, VecCmpZero, VecInt2Misc, VecFP3, VecFP2Misc, VecFPCmpZero, VecShiftImm, VecPermute, VecAcross}

// Bool is the Aux field read as a flag.
func (o VecOp) Bool() bool { return o.Aux != 0 }
