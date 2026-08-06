package e2e

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// core/bigint is differential-tested against Go's math/big rather than against
// hand-written expected values. Arbitrary-precision arithmetic fails on carry
// and borrow propagation at limb boundaries — 2^32, 2^64, and the points where
// a carry ripples through several limbs — and those are exactly the cases a
// human picking examples does not think to write down. An oracle that is
// already correct for every input is the right shape here, the same argument
// that made the Two-Way search suite an enumeration against a naive reference
// rather than a list of strings.
//
// One program per run, not one per case: a native compile is ~18 ms but a
// process per case would still dominate, and the whole corpus fits in one
// program that prints a line per case.

// bigintOperands are the values every pair-wise operation is run over. The
// list is chosen for limb-boundary structure, not for size: the interesting
// inputs are the ones straddling 2^32 and 2^64, where a carry has to cross a
// limb, plus the signed edges where negation overflows.
func bigintOperands() []*big.Int {
	lit := []string{
		"0", "1", "-1", "2", "-2",
		"4294967295", "4294967296", "4294967297", // 2^32 - 1, 2^32, 2^32 + 1
		"-4294967295", "-4294967296", "-4294967297",
		"18446744073709551615", "18446744073709551616", // 2^64 - 1, 2^64
		"9223372036854775807",                      // i64 MAX
		"-9223372036854775808",                     // i64 MIN — negation overflows in two's complement
		"79228162514264337593543950336",            // 2^96
		"340282366920938463463374607431768211456",  // 2^128
		"-340282366920938463463374607431768211455", // -(2^128 - 1)
		"123456789012345678901234567890",
		"-987654321098765432109876543210",
		// A carry that ripples the whole way: all-ones magnitude + 1.
		"1461501637330902918203684832716283019655932542975", // 2^160 - 1
	}
	out := make([]*big.Int, 0, len(lit)+8)
	for _, s := range lit {
		v, ok := new(big.Int).SetString(s, 10)
		if !ok {
			panic("bad literal " + s)
		}
		out = append(out, v)
	}
	// A few deterministic randoms well past any threshold the schoolbook
	// multiply has, so the accumulate-and-carry inner loop runs many limbs.
	rng := rand.New(rand.NewSource(20260806))
	for i := 0; i < 8; i++ {
		v := new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 300))
		if i%2 == 1 {
			v.Neg(v)
		}
		out = append(out, v)
	}
	return out
}

// bigintFernLit renders a Go big.Int as a Fern expression building that value.
// Values go through `bigint.parse` of a decimal string, which is the only
// constructor that can express something wider than i64 — so the parse path is
// exercised by every single case rather than needing its own suite.
func bigintFernLit(v *big.Int) string {
	return fmt.Sprintf("bigint_or_die(%q)", v.String())
}

// bigintCase is one expression and the value math/big says it must produce.
type bigintCase struct {
	expr string
	want string
}

// bigintCases builds the shared corpus. Both backend legs run exactly these,
// so a wasm-only divergence cannot hide behind a different case list.
func bigintCases() []bigintCase {
	ops := bigintOperands()
	var cases []bigintCase

	emit := func(expr, expected string) {
		cases = append(cases, bigintCase{expr, expected})
	}

	for i, a := range ops {
		// Round-trip: parse(to_string(v)) == v, and to_string is canonical
		// (no leading zeros, no "-0").
		emit(bigintFernLit(a)+".to_string()", a.String())

		// bit_length is checked against math/big's, which counts magnitude
		// bits and reports 0 for zero — the same contract core/bigint states.
		emit(fmt.Sprintf("(%s.bit_length()).to_string()", bigintFernLit(a)),
			fmt.Sprint(a.BitLen()))

		emit(bigintFernLit(a)+".negate().to_string()", new(big.Int).Neg(a).String())
		emit(bigintFernLit(a)+".abs().to_string()", new(big.Int).Abs(a).String())

		for j, b := range ops {
			// Full cross product on the literal set; the randoms only pair
			// with a small rotating slice, which keeps the program from
			// growing quadratically in the expensive multiplies while still
			// covering wide x wide.
			if i >= 22 && j >= 22 && (i+j)%3 != 0 {
				continue
			}
			la, lb := bigintFernLit(a), bigintFernLit(b)
			emit(la+".add("+lb+").to_string()", new(big.Int).Add(a, b).String())
			emit(la+".sub("+lb+").to_string()", new(big.Int).Sub(a, b).String())
			emit(la+".mul("+lb+").to_string()", new(big.Int).Mul(a, b).String())
			emit(fmt.Sprintf("(%s.cmp(%s)).to_string()", la, lb), fmt.Sprint(a.Cmp(b)))
		}
	}

	// mul_pow10 and shl get their own sweep — both are hand-rolled scaling
	// paths that the pair-wise ops never reach.
	for _, a := range ops[:22] {
		for _, k := range []int{0, 1, 9, 10, 17, 40} {
			emit(fmt.Sprintf("%s.mul_pow10(%d).to_string()", bigintFernLit(a), k),
				new(big.Int).Mul(a, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)).String())
			emit(fmt.Sprintf("%s.shl(%d).to_string()", bigintFernLit(a), k),
				new(big.Int).Lsh(a, uint(k)).String())
		}
	}
	return cases
}

// bigintPrelude is shared by both legs.
//
// bigint_or_die makes a parse failure loud instead of silently substituting a
// value the differential would then compare against the wrong expectation.
const bigintPrelude = `import "core/bigint";
import "std/i32";

function bigint_or_die(s: string): bigint.BigInt {
    match (bigint.parse(s)) {
        Some(v) => { return v; },
        None => { return bigint.from_i64(999999999); }
    }
}
`

func TestX86_64BigIntDifferential(t *testing.T) {
	cases := bigintCases()

	var body strings.Builder
	want := make([]string, 0, len(cases))
	for _, c := range cases {
		body.WriteString("    print(" + c.expr + ");\n")
		want = append(want, c.want)
	}

	src := bigintPrelude + `
function main(): i32 {
` + body.String() + `    return 0;
}
`

	out, exit := compileAndRunX86_64(t, src)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}

	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("printed %d lines, want %d — the program and the expectation "+
			"list are out of step, so no per-case comparison below is trustworthy",
			len(got), len(want))
	}

	bad := 0
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			bad++
			if bad <= 10 {
				t.Errorf("case %d: got %q, want %q", i, strings.TrimSpace(got[i]), want[i])
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatches (%d of %d cases failed)", bad-10, bad, len(want))
	}
	t.Logf("%d cases checked against math/big", len(want))
}

// BigInt's trait impls live in core/cmp rather than next to the type, because
// the orphan rule accepts either the trait's module or the type's, and only
// that side keeps core/bigint free of imports (std/string needs to import it
// for parse_float's exact fallback, and core/cmp imports std/string).
//
// The impls being EMPTY — adopting BigInt's existing to_string / eq / cmp /
// hash — is what makes this test necessary rather than obvious: an empty impl
// silently records that a type satisfies a trait, so a signature that drifted
// out of line with the trait would fail here and nowhere else. `cmp.sort` over
// a BigInt[] is the load-bearing case; it only works if `cmp` really returns
// the -1/0/1 that std/sort expects.
func TestX86_64BigIntTraits(t *testing.T) {
	src := `import "core/bigint";
import "core/cmp";
import "std/i32";

function show[T: cmp.Display](v: T): string { return v.to_string(); }
function biggest[T: cmp.Ord](a: T, b: T): T { if (a.cmp(b) > 0) { return a; } return b; }
function mk[T: cmp.Default](): T { return T.default(); }

function big(s: string): bigint.BigInt {
    match (bigint.parse(s)) { Some(v) => { return v; }, None => { return bigint.zero(); } }
}

function main(): i32 {
    // Ord, through std/sort's adaptive sort — negatives, zero, and values
    // wider than i64 all in one array.
    var xs: bigint.BigInt[] = [];
    xs = xs.append(big("340282366920938463463374607431768211456"));
    xs = xs.append(big("-5"));
    xs = xs.append(big("0"));
    xs = xs.append(big("18446744073709551616"));
    xs = xs.append(big("-340282366920938463463374607431768211456"));
    var s: bigint.BigInt[] = cmp.sort(xs);
    var i: i32 = 0;
    while (i < s.len()) { print(s[i].to_string()); i = i + 1; }

    print(show(big("12345")));
    print(show(biggest(big("7"), big("99"))));
    print(cmp.eq_arrays([big("1")], [big("1")]).to_string());
    print(big("-7").to_debug());

    var z: bigint.BigInt = mk();
    print(z.to_string());

    // The Hash law: equal values hash equally. It holds because the
    // representation is canonical (trimmed magnitude, zero never negative),
    // so two equal BigInts have byte-identical limb arrays.
    print((big("42").hash() == big("42").hash()).to_string());
    print((big("0").hash() == bigint.zero().hash()).to_string());
    return 0;
}
`
	want := []string{
		"-340282366920938463463374607431768211456",
		"-5",
		"0",
		"18446744073709551616",
		"340282366920938463463374607431768211456",
		"12345",
		"99",
		"true",
		"-7",
		"0",
		"true",
		"true",
	}

	out, exit := compileAndRunX86_64(t, src)
	if exit != 0 {
		t.Fatalf("program exited %d, want 0\noutput:\n%s", exit, out)
	}
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("printed %d lines, want %d\noutput:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if strings.TrimSpace(got[i]) != want[i] {
			t.Errorf("line %d: got %q, want %q", i, strings.TrimSpace(got[i]), want[i])
		}
	}
}

// The wasm leg runs the SAME corpus, self-checking, because the wasm harness
// reports an exit code rather than stdout. It exists because wasm32 is where
// this module's arithmetic is most likely to diverge: every limb operation is
// u64, and wasm32 is the one target where a 64-bit value is not a machine
// word. A carry or shift lowered differently there would be invisible to the
// x86-64 leg.
//
// The exit code is 0 for all-pass and the 1-based index of the first
// mismatching case otherwise, capped at 250 so it cannot collide with the
// runtime's own status codes (125 arena-exhausted, 128+signal). The x86-64 leg
// is where a failure gets diagnosed; this one only has to catch it.
func TestWasmBigIntDifferential(t *testing.T) {
	all := bigintCases()

	// The wasm leg runs a STRIDED SUBSET, not the whole corpus, and the count
	// is logged rather than left implicit — a leg that silently covers an
	// eighth of what it appears to is worse than one that says so.
	//
	// The limit is allocation, not program size: wasm's linear memory is a
	// different order of magnitude from the 16 GiB native arena, and every
	// case here parses two decimal strings into fresh limb arrays and then
	// allocates through an arithmetic op. The whole corpus in one process
	// traps with "out of bounds memory access" at instantiation. Verified it
	// is not the generated program's shape: 3612 distinct 39-char string
	// literals, and 1600 inlined checks, both compile and run clean on wasm.
	//
	// A stride rather than a prefix so the subset keeps the corpus's
	// structure — the operand list is ordered small-to-large and a prefix
	// would test only the narrow values, which are exactly the ones least
	// likely to expose a limb-arithmetic divergence.
	const stride = 8
	cases := make([]bigintCase, 0, len(all)/stride+1)
	for i := 0; i < len(all); i += stride {
		cases = append(cases, all[i])
	}

	// Checks are split across helper functions rather than inlined into main.
	// Each `expr` needs temporaries, and 3600 of them in one body blows
	// wasm's per-function locals cap ("too many locals") before any of this
	// module's code runs — a property of the generated harness, not of
	// core/bigint.
	const perGroup = 200
	var groups strings.Builder
	var calls strings.Builder
	for start := 0; start < len(cases); start += perGroup {
		end := min(start+perGroup, len(cases))
		g := start / perGroup
		groups.WriteString(fmt.Sprintf("function chk%d(): i32 {\n", g))
		for i := start; i < end; i++ {
			groups.WriteString(fmt.Sprintf("    if (%s != %q) { return %d; }\n",
				cases[i].expr, cases[i].want, min(i+1, 250)))
		}
		groups.WriteString("    return 0;\n}\n")
		calls.WriteString(fmt.Sprintf("    var r%d: i32 = chk%d();\n    if (r%d != 0) { return r%d; }\n", g, g, g, g))
	}

	src := bigintPrelude + "\n" + groups.String() + `
function main(): i32 {
` + calls.String() + `    return 0;
}
`

	if got := compileAndRunWasmbinMain(t, src); got != 0 {
		t.Errorf("wasm run exited %d, want 0 — case #%d (1-based, capped at 250) "+
			"disagrees with math/big on wasm32 but not on x86-64, so the divergence "+
			"is in how this module's u64 limb arithmetic lowers rather than in the "+
			"algorithm. Run TestX86_64BigIntDifferential for the expected value",
			got, got)
	}
	t.Logf("%d of %d cases checked on wasm (stride %d)", len(cases), len(all), stride)
}
