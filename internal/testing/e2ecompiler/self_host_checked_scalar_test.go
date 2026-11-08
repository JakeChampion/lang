package e2ecompiler

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Arbitrary-precision arithmetic supplies results independently of IR lowering.
func checkedScalarExpected(op string, a, b, low, high *big.Int, width int) (*big.Int, bool) {
	v := new(big.Int)
	switch op {
	case "+?":
		v.Add(a, b)
	case "-?":
		v.Sub(a, b)
	case "*?":
		v.Mul(a, b)
	case "/?", "%?":
		if b.Sign() == 0 || a.Cmp(low) == 0 && low.Sign() < 0 && b.Cmp(big.NewInt(-1)) == 0 {
			return v, false
		}
		if op == "/?" {
			v.Quo(a, b)
		} else {
			v.Rem(a, b)
		}
	case "<<?", ">>?":
		if !b.IsUint64() || b.Uint64() >= uint64(width) {
			return v, false
		}
		if op == "<<?" {
			v.Lsh(a, uint(b.Uint64()))
		} else {
			v.Rsh(a, uint(b.Uint64()))
		}
	}
	if v.Cmp(low) < 0 || v.Cmp(high) > 0 {
		return new(big.Int), false
	}
	return v, true
}

func checkedScalarSource() string {
	var src strings.Builder
	ops := []string{"+?", "-?", "*?", "/?", "%?", "<<?", ">>?"}
	types := []struct {
		name   string
		bits   int
		signed bool
	}{{"u8", 8, false}, {"i32", 32, true}, {"u32", 32, false}, {"i64", 64, true}, {"u64", 64, false}}
	for _, ty := range types {
		fmt.Fprintf(&src, "struct Case_%s { a: %s, b: %s, want: %s[], present: boolean[] }\n", ty.name, ty.name, ty.name, ty.name)
		fmt.Fprintf(&src, "@noinline function check_%s(row: Case_%s): i32 {\n", ty.name, ty.name)
		for i, op := range ops {
			fmt.Fprintf(&src, `
  let mark_%d = __heap_alloc_count();
  match (row.a %s row.b) {
    Some(v) => { if (!row.present[%d] || v != row.want[%d]) { print("%s %s payload"); return 1; } },
    None => { if (row.present[%d]) { print("%s %s presence"); return 2; } }
  }
  if (__heap_alloc_count() != mark_%d) { print("%s %s allocation"); return 3; }
`, i, op, i, i, ty.name, op, i, ty.name, op, i, ty.name, op)
		}
		src.WriteString("return 0; }\n")
	}
	src.WriteString(`
@noinline function escaped(a: u32, b: u32): Option[u32] { return a +? b; }
@noinline function inspect(value: Option[u32], expected: u32): boolean {
  match (value) { Some(v) => { return v == expected; }, None => { return false; } }
}
function main(): i32 {
`)
	for _, ty := range types {
		low := new(big.Int)
		high := new(big.Int).Lsh(big.NewInt(1), uint(ty.bits))
		if ty.signed {
			high.Rsh(high, 1)
			low.Neg(high)
		}
		high.Sub(high, big.NewInt(1))
		edges := []*big.Int{low, high, big.NewInt(0), big.NewInt(1), big.NewInt(2), big.NewInt(int64(ty.bits - 1)), big.NewInt(int64(ty.bits)), big.NewInt(int64(ty.bits + 1))}
		if ty.signed {
			edges = append(edges, big.NewInt(-1), big.NewInt(-2), new(big.Int).Add(low, big.NewInt(1)))
		}
		pairs := [][2]*big.Int{}
		for _, a := range edges {
			for _, b := range edges {
				pairs = append(pairs, [2]*big.Int{a, b})
			}
		}
		rng := rand.New(rand.NewSource(14720))
		span := new(big.Int).Sub(high, low)
		span.Add(span, big.NewInt(1))
		for range 32 {
			a := new(big.Int).SetUint64(rng.Uint64())
			b := new(big.Int).SetUint64(rng.Uint64())
			a.Add(a.Mod(a, span), low)
			b.Add(b.Mod(b, span), low)
			pairs = append(pairs, [2]*big.Int{a, b})
		}
		fmt.Fprintf(&src, "let rows_%s: Case_%s[] = [\n", ty.name, ty.name)
		for _, pair := range pairs {
			want, present := []string{}, []string{}
			for _, op := range ops {
				v, ok := checkedScalarExpected(op, pair[0], pair[1], low, high, ty.bits)
				want = append(want, v.String()+ty.name)
				present = append(present, fmt.Sprint(ok))
			}
			fmt.Fprintf(&src, "Case_%s { a: %s%s, b: %s%s, want: [%s], present: [%s] },\n", ty.name, pair[0], ty.name, pair[1], ty.name, strings.Join(want, ","), strings.Join(present, ","))
		}
		fmt.Fprintf(&src, "];\nfor row in rows_%s { let code = check_%s(row); if (code != 0) { return code; } }\n", ty.name, ty.name)
	}
	src.WriteString(`
  let saved = [escaped(4u32, 5u32), escaped(4294967295u32, 1u32)];
  if (!inspect(saved[0], 9u32)) { return 4; }
  match (saved[1]) { Some(_) => { return 5; }, None => {} }
  let carried = 4294967294u32 +? 1u32;
  let original = carried;
  for i in 0..4 { if (i % 2 == 0) { carried = None; } else { carried = Some(i as u32); } }
  if (!inspect(original, 4294967295u32) || !inspect(carried, 3u32)) { return 6; }
  if (__rc_underflow_count() != 0) { return 7; }
  return 0;
}
`)
	return src.String()
}

func TestSelfHostCheckedArithmeticScalarOptions(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			bin := e2eharness.CompileSelfHostSource(t, target, checkedScalarSource(), []string{"FERN_LEAKCHECK=1", "FERN_IR_VERIFY=1"})
			out, err := runScaleTarget(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("checked arithmetic: %v\n%s", err, out)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("checked scalar ownership: %v\n%s", err, out)
			}
		})
	}
}
