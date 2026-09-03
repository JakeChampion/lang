// Package fdlibm carries the numeric tables behind the f64 transcendental
// runtime helpers — __fern_{exp,log,sin,cos,pow}_f64 — as one source of truth
// for every backend that emits them.
//
// The kernels are fdlibm's, and the accuracy is the point: measured against
// the correctly-rounded reference over 20k samples per range they land at
// <= 1 ulp, where the Taylor kernels these replaced measure 3.2e10 ulp (sin),
// 4.5e7 (exp) and 9844 (log) while their own comments claimed "a few ulp".
//
// The tables lived in five copies before this package — internal/codegen's
// arm64, arm64ssa, wasmbin and x86_64, plus the three self-host emitters —
// which is the parallel-emit drift hazard CLAUDE.md warns about, and it has
// already cost real accuracy once: #6313 exists because three copies still
// carried the old math after two prior PRs fixed the others. The emit layers
// stay deliberately parallel; the numbers they emit do not.
//
// The self-host emitters cannot import Go, so they keep their own literals and
// selfhost_parity_test.go pins them to this table bit for bit.
//
// Three properties of the table are load-bearing rather than incidental:
//
//   - The reductions are Cody-Waite. A constant is split into a head with its
//     low mantissa bits zeroed plus a tail, so `x - k*head` is EXACT and only
//     the far smaller `k*tail` rounds. Reducing against a single rounded pi/2
//     is what made the old sin lose ~7 digits by |x| ~ 10.
//   - pi/2 is carried as THREE 33-bit chunks (~99 bits) for |x| < 2^20. Two
//     leave sin(pi) 285k ulp out: near a zero of sin the reduced argument IS
//     the answer, so the reduction's absolute error becomes the result's
//     relative error. A fourth chunk makes it worse — it perturbs the
//     cancellation the third one sets up.
//   - expovf/expunf bound exp BEFORE the 2^k reconstruction, which builds the
//     exponent field as (k+1023)<<52 and otherwise overflows silently into the
//     SIGN bit — exp(1000) came out as -6.1e-183 rather than +Inf.
package fdlibm

import "strconv"

// A Coeff is one entry of the coefficient table. Emitters that load from
// .rodata spell the label `.Lfc_` + Name and the datum `.double ` + Text;
// emitters that inline the constant use Val, which is Text parsed, so the two
// spellings cannot drift.
type Coeff struct {
	Name string
	Text string
	Val  float64
}

// Coeffs is the coefficient table in emission order. The order is part of the
// contract: arm64 addresses entries by their byte offset within it.
var Coeffs = []Coeff{
	def("one", "1.0"),
	def("half", "0.5"),
	def("two", "2.0"),
	def("sqrt2", "1.4142135623730951"),

	// Cody-Waite splits for exp (ln2) and for the trig quadrant count (2/pi).
	def("invln2", "1.44269504088896338700e+00"),
	def("ln2hi", "6.93147180369123816490e-01"),
	def("ln2lo", "1.90821492927058770002e-10"),
	def("2opi", "6.36619772367581382433e-01"),

	// pi/2 as three 33-bit chunks. The head carries 22 trailing zero mantissa
	// bits, so `x - k*pio2h` is exact only while k fits in 22 bits; past that
	// the reduction is __fern_rem_pio2_large's.
	def("pio2h", "1.57079632673412561417e+00"),
	def("pio2m", "6.07710050630396597660e-11"),
	def("pio2l", "2.02226624879595063154e-21"),

	// pi/2 as an unevaluated double-double, plus the two scales that turn
	// __fern_rem_pio2_large's 126-bit fraction into a double.
	def("pio2hi", "1.5707963267948966"),
	def("pio2lo", "6.123233995736766e-17"),
	def("2m62", "2.168404344971009e-19"),
	def("2m115", "2.407412430484045e-35"),

	// sin kernel, |r| <= pi/4.
	def("s1", "-1.66666666666666324348e-01"),
	def("s2", "8.33333333332248946124e-03"),
	def("s3", "-1.98412698298579493134e-04"),
	def("s4", "2.75573137070700676789e-06"),
	def("s5", "-2.50507602534068634195e-08"),
	def("s6", "1.58969099521155010221e-10"),

	// cos kernel, |r| <= pi/4.
	def("c1", "4.16666666666666019037e-02"),
	def("c2", "-1.38888888888741095749e-03"),
	def("c3", "2.48015872894767294178e-05"),
	def("c4", "-2.75573143513906633035e-07"),
	def("c5", "2.08757232129817482790e-09"),
	def("c6", "-1.13596475577881948265e-11"),

	// exp kernel.
	def("p1", "1.66666666666666019037e-01"),
	def("p2", "-2.77777777770155933842e-03"),
	def("p3", "6.61375632143793436117e-05"),
	def("p4", "-1.65339022054652515390e-06"),
	def("p5", "4.13813679705723846039e-08"),

	// log kernel.
	def("lg1", "6.666666666666735130e-01"),
	def("lg2", "3.999999999940941908e-01"),
	def("lg3", "2.857142874366239149e-01"),
	def("lg4", "2.222219843214978396e-01"),
	def("lg5", "1.818357216161805012e-01"),
	def("lg6", "1.531383769920937332e-01"),
	def("lg7", "1.479819860511658591e-01"),

	// exp's finite range.
	def("expovf", "709.782712893383973096"),
	def("expunf", "-745.133219101941108420"),
}

func def(name, text string) Coeff {
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		panic("fdlibm: coefficient " + name + " is not a double: " + err.Error())
	}
	return Coeff{Name: name, Text: text, Val: v}
}

// The table's entries by name, for emitters that inline a constant rather than
// loading it from .rodata. Named so a typo is a compile error.
var (
	One     = at("one")
	Half    = at("half")
	Two     = at("two")
	Sqrt2   = at("sqrt2")
	InvLn2  = at("invln2")
	Ln2Hi   = at("ln2hi")
	Ln2Lo   = at("ln2lo")
	TwoOPi  = at("2opi")
	Pio2H   = at("pio2h")
	Pio2M   = at("pio2m")
	Pio2L   = at("pio2l")
	Pio2Hi  = at("pio2hi")
	Pio2Lo  = at("pio2lo")
	TwoM62  = at("2m62")
	TwoM115 = at("2m115")
	S1      = at("s1")
	S2      = at("s2")
	S3      = at("s3")
	S4      = at("s4")
	S5      = at("s5")
	S6      = at("s6")
	C1      = at("c1")
	C2      = at("c2")
	C3      = at("c3")
	C4      = at("c4")
	C5      = at("c5")
	C6      = at("c6")
	P1      = at("p1")
	P2      = at("p2")
	P3      = at("p3")
	P4      = at("p4")
	P5      = at("p5")
	Lg1     = at("lg1")
	Lg2     = at("lg2")
	Lg3     = at("lg3")
	Lg4     = at("lg4")
	Lg5     = at("lg5")
	Lg6     = at("lg6")
	Lg7     = at("lg7")
	ExpOvf  = at("expovf")
	ExpUnf  = at("expunf")
)

// at returns the named coefficient's value.
func at(name string) float64 {
	for _, c := range Coeffs {
		if c.Name == name {
			return c.Val
		}
	}
	panic("fdlibm: no coefficient named " + name)
}

// Off is the byte offset of the named coefficient within an emitted table.
func Off(name string) int {
	for i, c := range Coeffs {
		if c.Name == name {
			return i * 8
		}
	}
	panic("fdlibm: no coefficient named " + name)
}

// TwoOverPiBits is 2/pi in binary, MSB-first, one limb per 64 fraction bits
// starting at 2^-1 in limb 1 — the window Payne-Hanek indexes with the
// argument's own exponent. The leading zero limb lets that index start above
// 2^-1 without a bounds test; the length covers the largest finite double.
var TwoOverPiBits = [...]uint64{
	0x0000000000000000, 0xa2f9836e4e441529, 0xfc2757d1f534ddc0,
	0xdb6295993c439041, 0xfe5163abdebbc561, 0xb7246e3a424dd2e0,
	0x06492eea09d1921c, 0xfe1deb1cb129a73e, 0xe88235f52ebb4484,
	0xe99c7026b45f7e41, 0x3991d639835339f4, 0x9c845f8bbdf9283b,
	0x1ff897ffde05980f, 0xef2f118b5a0a6d1f, 0x6d367ecf27cb09b7,
	0x4f463f669e5fea2d, 0x7527bac7ebe5f17b, 0x3d0739f78a5292ea,
	0x6bfb5fb11f8d5d08, 0x56033046fc7b6bab, 0xf0cfbc209af4361d,
}
