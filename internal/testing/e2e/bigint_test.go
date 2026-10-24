package e2e

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

func bigintOperands() []*big.Int {
	lit := []string{
		"0", "1", "-1", "2", "-2",
		"4294967295", "4294967296", "4294967297",
		"-4294967295", "-4294967296", "-4294967297",
		"18446744073709551615", "18446744073709551616",
		"9223372036854775807",
		"-9223372036854775808",
		"79228162514264337593543950336",
		"340282366920938463463374607431768211456",
		"-340282366920938463463374607431768211455",
		"123456789012345678901234567890",
		"-987654321098765432109876543210",
		"1461501637330902918203684832716283019655932542975",
	}
	out := make([]*big.Int, 0, len(lit)+8)
	for _, s := range lit {
		v, ok := new(big.Int).SetString(s, 10)
		if !ok {
			panic("bad literal " + s)
		}
		out = append(out, v)
	}
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

func bigintFernLit(v *big.Int) string {
	return fmt.Sprintf("bigint_or_die(%q)", v.String())
}

type bigintCase struct {
	expr string
	want string
}

func bigintCases() []bigintCase {
	ops := bigintOperands()
	var cases []bigintCase

	emit := func(expr, expected string) {
		cases = append(cases, bigintCase{expr, expected})
	}

	for i, a := range ops {
		emit(bigintFernLit(a)+".to_string()", a.String())

		emit(fmt.Sprintf("(%s.bit_length()).to_string()", bigintFernLit(a)),
			fmt.Sprint(a.BitLen()))

		emit(bigintFernLit(a)+".negate().to_string()", new(big.Int).Neg(a).String())
		emit(bigintFernLit(a)+".abs().to_string()", new(big.Int).Abs(a).String())

		for j, b := range ops {
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

	for _, a := range ops[:22] {
		for _, k := range []int{0, 1, 9, 10, 17, 40} {
			emit(fmt.Sprintf("%s.mul_pow10(%d).to_string()", bigintFernLit(a), k),
				new(big.Int).Mul(a, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)).String())
			emit(fmt.Sprintf("%s.shl(%d).to_string()", bigintFernLit(a), k),
				new(big.Int).Lsh(a, uint(k)).String())
			shifted := new(big.Int).Rsh(new(big.Int).Abs(a), uint(k))
			if a.Sign() < 0 {
				shifted.Neg(shifted)
			}
			emit(fmt.Sprintf("%s.shr(%d).to_string()", bigintFernLit(a), k), shifted.String())
		}
	}

	for i, a := range ops {
		for j, b := range ops {
			if b.Sign() == 0 || (i >= 22 && j >= 22 && (i+j)%3 != 0) {
				continue
			}
			q, r := new(big.Int).QuoRem(a, b, new(big.Int))
			la, lb := bigintFernLit(a), bigintFernLit(b)
			emit("(("+la+").divmod("+lb+")).0.to_string()", q.String())
			emit("(("+la+").divmod("+lb+")).1.to_string()", r.String())
		}
	}

	for _, a := range ops[:12] {
		la := bigintFernLit(a)
		emit("(("+la+").divmod(bigint.zero())).0.to_string()", "0")
		emit("(("+la+").divmod(bigint.zero())).1.to_string()", a.String())
	}

	for _, a := range ops[:22] {
		la := bigintFernLit(a)
		low := new(big.Int).And(new(big.Int).Abs(a), new(big.Int).SetUint64(^uint64(0)))
		emit("("+la+".low_u64()).to_string()", low.String())
		for _, i := range []int{0, 1, 31, 32, 63, 64, 95, 200} {
			emit(fmt.Sprintf("(%s.bit(%d)).to_string()", la, i), fmt.Sprint(new(big.Int).Abs(a).Bit(i) == 1))
		}
		for _, n := range []int{0, 1, 2, 3, 7} {
			emit(fmt.Sprintf("%s.pow(%d).to_string()", la, n), new(big.Int).Exp(a, big.NewInt(int64(n)), nil).String())
		}
	}
	emit("bigint.from_u64((0 as u64) - (1 as u64)).to_string()", "18446744073709551615")
	emit("bigint.from_u64(9223372036854775808 as u64).to_string()", "9223372036854775808")
	emit("bigint.from_u64(4294967296 as u64).to_string()", "4294967296")
	emit("bigint.from_u64(0 as u64).to_string()", "0")
	return cases
}

const bigintPrelude = `import "core/bigint";
import "std/i32";
import "std/u64";

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
    // Ord, through core/cmp's adaptive sort — negatives, zero, and values
    // wider than i64 all in one array.
    let xs: bigint.BigInt[] = [];
    xs = xs.append(big("340282366920938463463374607431768211456"));
    xs = xs.append(big("-5"));
    xs = xs.append(big("0"));
    xs = xs.append(big("18446744073709551616"));
    xs = xs.append(big("-340282366920938463463374607431768211456"));
    let s: bigint.BigInt[] = cmp.sort(xs);
    let i: i32 = 0;
    while (i < s.len()) { print(s[i].to_string()); i = i + 1; }

    print(show(big("12345")));
    print(show(biggest(big("7"), big("99"))));
    print(cmp.eq_arrays([big("1")], [big("1")]).to_string());
    print(big("-7").to_debug());

    let z: bigint.BigInt = mk();
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

func TestWasmBigIntDifferential(t *testing.T) {
	all := bigintCases()

	const stride = 8
	cases := make([]bigintCase, 0, len(all)/stride+1)
	for i := 0; i < len(all); i += stride {
		cases = append(cases, all[i])
	}

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
		calls.WriteString(fmt.Sprintf("    let r%d: i32 = chk%d();\n    if (r%d != 0) { return r%d; }\n", g, g, g, g))
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
