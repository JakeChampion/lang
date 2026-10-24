package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

var rangePatternCases = []struct {
	name string
	src  string
	want int
}{
	{
		name: "exclusive",
		src: `function cls(x: i32): i32 {
  match (x) {
    1..5 => { return 1; },
    5..10 => { return 2; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(3) * 100 + cls(5) * 10 + cls(9) + cls(20); }`,
		want: 122,
	},
	{
		name: "inclusive",
		src: `function cls(x: i32): i32 {
  match (x) {
    1..=5 => { return 1; },
    6..=10 => { return 2; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(5) * 100 + cls(6) * 10 + cls(10) + cls(11); }`,
		want: 122,
	},
	{
		name: "expr_form",
		src: `function cls(x: i32): i32 {
  return match (x) { 0..10 => 1, 10..20 => 2, _ => 0 };
}
function main(): i32 { return cls(5) * 100 + cls(15) * 10 + cls(25); }`,
		want: 120,
	},
	{
		name: "range_literal_mix",
		src: `function cls(x: i32): i32 {
  match (x) {
    0 => { return 9; },
    1..10 => { return 1; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(0) * 10 + cls(5) + cls(50); }`,
		want: 91,
	},
	{
		name: "guarded_range",
		src: `function cls(x: i32): i32 {
  match (x) {
    1..100 when x % 2 == 0 => { return 1; },
    1..100 => { return 2; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(4) * 100 + cls(5) * 10 + cls(200); }`,
		want: 120,
	},
	{
		name: "i64_range",
		src: `function cls(x: i64): i32 {
  match (x) {
    1..100 => { return 1; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return cls(50 as i64) * 10 + cls(500 as i64); }`,
		want: 10,
	},
}

func TestRangePatternX86_64(t *testing.T) {
	for _, tc := range rangePatternCases {
		t.Run(tc.name, func(t *testing.T) {
			oracle := runInterpByte(t, tc.src)
			if oracle != tc.want {
				t.Fatalf("interp oracle = %d, want %d", oracle, tc.want)
			}
			_, code := compileAndRunX86_64(t, tc.src)
			if code != tc.want {
				t.Errorf("native x86-64 = %d, want %d (interp oracle agrees at %d)", code, tc.want, oracle)
			}
		})
	}
}

func TestRangePatternWasm(t *testing.T) {
	for _, name := range []string{"exclusive", "expr_form"} {
		tc := rangePatternCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRangePatternUnsignedRejected(t *testing.T) {
	src := `function cls(x: u32): i32 {
  match (x) { 1..5 => { return 1; }, _ => { return 0; } }
  return 0 - 1;
}
function main(): i32 { return 0; }`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatal("expected E035 for a range pattern on an unsigned scrutinee, got no error")
	}
}

func rangePatternCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range rangePatternCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no range-pattern case named %q", name)
	return rangePatternCases[0]
}
