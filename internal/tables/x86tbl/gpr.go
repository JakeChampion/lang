package x86tbl

type GroupOp struct {
	Spellings []string
	Ext       byte
}

type Group struct {
	Name     string
	ATTProbe string
	Ops      []GroupOp
}

func (g Group) Spellings() []string {
	var out []string
	for _, op := range g.Ops {
		out = append(out, op.Spellings...)
	}
	return out
}

var ALU = Group{"alu", "%sq %rcx, %rax", []GroupOp{
	{[]string{"add"}, 0}, {[]string{"or"}, 1}, {[]string{"adc"}, 2}, {[]string{"sbb"}, 3},
	{[]string{"and"}, 4}, {[]string{"sub"}, 5}, {[]string{"xor"}, 6}, {[]string{"cmp"}, 7},
}}

var Shift = Group{"shift", "%sq $3, %rax", []GroupOp{
	{[]string{"rol"}, 0}, {[]string{"ror"}, 1}, {[]string{"rcl"}, 2}, {[]string{"rcr"}, 3},
	{[]string{"shl", "sal"}, 4}, {[]string{"shr"}, 5}, {[]string{"sar"}, 7},
}}

var Unary = Group{"unary", "%sq %rcx", []GroupOp{
	{[]string{"not"}, 2}, {[]string{"neg"}, 3}, {[]string{"mul"}, 4},
	{[]string{"imul"}, 5}, {[]string{"div"}, 6}, {[]string{"idiv"}, 7},
}}

var IncDec = Group{"incdec", "%sq %rax", []GroupOp{{[]string{"inc"}, 0}, {[]string{"dec"}, 1}}}

var BitTest = Group{"bittest", "%sq %rcx, %rax", []GroupOp{
	{[]string{"bt"}, 0}, {[]string{"bts"}, 1}, {[]string{"btr"}, 2}, {[]string{"btc"}, 3},
}}

var Groups = []Group{ALU, Shift, Unary, IncDec, BitTest}

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
