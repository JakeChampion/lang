// Package x86tbl is the single source of truth for the x86-64 encoding tables
// both assemblers need (#7903).
//
// There are two x86-64 assemblers — internal/native/x86_64 in Go and
// examples/self_host/x86_native.fern in Fern — and they must agree byte for
// byte. Every drift found so far has been the same shape: not an encoding-logic
// bug, but a VOCABULARY one, where one side reaches a mnemonic or an operand
// form the other does not (#8000, 63 mnemonics; #8020, 13; #8071, 23 condition
// spellings). So this package holds the vocabulary, the Go assembler consumes
// it directly, and cmd/x86tblgen writes the Fern side from it.
//
// The mechanism follows cmd/floattablegen and cmd/unicodegen, which already
// generate .fern between marker comments: the self-host language has
// module-level `const` for scalars only, so a generated Fern table has to be
// code rather than data, and a staleness test is what keeps the committed
// output honest.
package x86tbl

// Cond is one 4-bit condition code together with every suffix spelling GNU as
// accepts for it. The aliases are not decoration: `jnbe` and `ja` are the same
// branch, and compiler output uses both, so an assembler that knows one and
// not the other drops instructions it was handed.
type Cond struct {
	Code byte
	// Spellings are ordered canonical-first. jCC, setCC and cmovCC share this
	// vocabulary exactly — their opcodes differ only by the base each adds the
	// code to (0x80, 0x90 and 0x40 on the two-byte page).
	Spellings []string
}

// Conds is the condition table, in code order.
//
// Pinned against GNU as, not the SDM's prose: `as` is what compiler output is
// written for, and it accepts spellings (`nae`, `nbe`, `nge`, `nle`) that a
// reading of the manual's condition names alone would not produce.
var Conds = []Cond{
	{0, []string{"o"}},
	{1, []string{"no"}},
	{2, []string{"b", "c", "nae"}},
	{3, []string{"ae", "nb", "nc"}},
	{4, []string{"e", "z"}},
	{5, []string{"ne", "nz"}},
	{6, []string{"be", "na"}},
	{7, []string{"a", "nbe"}},
	{8, []string{"s"}},
	{9, []string{"ns"}},
	{10, []string{"p"}},
	{11, []string{"np"}},
	{12, []string{"l", "nge"}},
	{13, []string{"ge", "nl"}},
	{14, []string{"le", "ng"}},
	{15, []string{"g", "nle"}},
}

// CondCodes is the flat spelling-to-code map the Go assembler dispatches on.
func CondCodes() map[string]byte {
	m := make(map[string]byte, 28)
	for _, c := range Conds {
		for _, s := range c.Spellings {
			m[s] = c.Code
		}
	}
	return m
}

// CondSpellings is every spelling in table order — the enumeration the parity
// gates walk, so that no spelling can be excluded from one without being
// dropped from the other.
func CondSpellings() []string {
	out := make([]string, 0, 28)
	for _, c := range Conds {
		out = append(out, c.Spellings...)
	}
	return out
}
