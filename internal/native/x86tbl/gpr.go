package x86tbl

// GroupOp is one member of a ModRM.reg-extension family: the spellings gas
// accepts for it and the /digit (or index) that selects it.
type GroupOp struct {
	Spellings []string
	Ext       byte
}

// Group is a family of general-purpose instructions that share an opcode
// row and differ only in a 3-bit field — the shape the SDM's "group"
// tables describe and both assemblers dispatch by. The vocabulary of the
// family lives here; the encoding logic that consumes the extension stays
// hand-written on each side, since it is the same few lines and never the
// part that drifted.
type Group struct {
	Name string
	// Probe and ATTProbe are one representative instruction of the family
	// in each dialect, %s standing for the mnemonic: the test inventory the
	// table-row differential assembles through both assemblers.
	Probe, ATTProbe string
	Ops             []GroupOp
}

// Ext returns the extension for a base mnemonic (no AT&T suffix).
func (g Group) Ext(mnem string) (byte, bool) {
	for _, op := range g.Ops {
		for _, s := range op.Spellings {
			if s == mnem {
				return op.Ext, true
			}
		}
	}
	return 0, false
}

// Spellings is every spelling the group accepts, in table order.
func (g Group) Spellings() []string {
	var out []string
	for _, op := range g.Ops {
		out = append(out, op.Spellings...)
	}
	return out
}

// ALU is group 1 (80/81/83 /digit with an immediate, 00+8*digit /r
// otherwise): the digit is also the opcode row.
var ALU = Group{"alu", "%s rax, rcx", "%sq %rcx, %rax", []GroupOp{
	{[]string{"add"}, 0}, {[]string{"or"}, 1}, {[]string{"adc"}, 2}, {[]string{"sbb"}, 3},
	{[]string{"and"}, 4}, {[]string{"sub"}, 5}, {[]string{"xor"}, 6}, {[]string{"cmp"}, 7},
}}

// Shift is group 2 (C0/C1 ib, D0/D1 by one, D2/D3 by cl). `sal` is gas's
// alias for shl: the SDM lists /6 as a second SAL encoding, but gas emits
// /4 for both spellings and so do we. /6 is deliberately absent.
var Shift = Group{"shift", "%s rax, 3", "%sq $3, %rax", []GroupOp{
	{[]string{"rol"}, 0}, {[]string{"ror"}, 1}, {[]string{"rcl"}, 2}, {[]string{"rcr"}, 3},
	{[]string{"shl", "sal"}, 4}, {[]string{"shr"}, 5}, {[]string{"sar"}, 7},
}}

// Unary is group 3 (F6/F7 /digit), the one-operand arithmetic. `test` is
// its /0 but takes two operands and has its own encoder, so it is not
// listed; `imul` here is the one-operand form, the two- and three-operand
// forms being 0F AF and 69/6B.
var Unary = Group{"unary", "%s rcx", "%sq %rcx", []GroupOp{
	{[]string{"not"}, 2}, {[]string{"neg"}, 3}, {[]string{"mul"}, 4},
	{[]string{"imul"}, 5}, {[]string{"div"}, 6}, {[]string{"idiv"}, 7},
}}

// IncDec is group 4/5's inc and dec (FE/FF /0 and /1).
var IncDec = Group{"incdec", "%s rax", "%sq %rax", []GroupOp{{[]string{"inc"}, 0}, {[]string{"dec"}, 1}}}

// BitTest is the bt family. The extension is an INDEX rather than a digit:
// the register form is 0F A3 + 8*index and the immediate form is 0F BA
// /(4+index).
var BitTest = Group{"bittest", "%s rax, rcx", "%sq %rcx, %rax", []GroupOp{
	{[]string{"bt"}, 0}, {[]string{"bts"}, 1}, {[]string{"btr"}, 2}, {[]string{"btc"}, 3},
}}

// Groups is every family, for the gates that enumerate the vocabulary.
var Groups = []Group{ALU, Shift, Unary, IncDec, BitTest}

// Lockable is the set of base mnemonics a `lock` prefix may precede: the
// read-modify-write members of the groups above — every ALU op but cmp,
// inc/dec, not/neg, the bit ops that write, but not bt — plus the exchange
// family. gas refuses `lock` on anything else, and a prefix on an
// instruction that ignores it would be a silent race rather than an error,
// so both assemblers refuse it too.
func Lockable() map[string]bool {
	m := map[string]bool{"xadd": true, "cmpxchg": true, "xchg": true}
	for _, s := range ALU.Spellings() {
		if s != "cmp" {
			m[s] = true
		}
	}
	for _, s := range IncDec.Spellings() {
		m[s] = true
	}
	m["not"], m["neg"] = true, true
	for _, s := range BitTest.Spellings() {
		if s != "bt" {
			m[s] = true
		}
	}
	return m
}

// LockableSpellings is Lockable as a sorted list, for generated output
// that must not reorder between runs.
func LockableSpellings() []string {
	m := Lockable()
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sortStrings(out)
	return out
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
