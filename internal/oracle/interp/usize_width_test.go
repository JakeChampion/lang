package interp

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const usizeWidthSrc = `
function wide(a: i64): usize {
  return a as usize;
}

function borrow(): usize {
  return wide(0) - wide(1);
}

function carry(): usize {
  return wide(4294967295) + wide(1);
}

function shifted(): usize {
  return wide(1) << 33;
}

function halved(): usize {
  return borrow() >> 1;
}

function negated(): usize {
  return -wide(1);
}

function saturated(): usize {
  return 10000000000.0 as usize;
}

function main(): i32 {
  return 0;
}
`

func TestUsizeWrapsAtTheHostPointerWidth(t *testing.T) {
	cases := []struct {
		fn     string
		want64 uint64
		want32 uint64
	}{
		{"borrow", 18446744073709551615, 4294967295},
		{"carry", 4294967296, 0},
		{"shifted", 8589934592, 2},
		{"halved", 9223372036854775807, 2147483647},
		{"negated", 18446744073709551615, 4294967295},
		{"saturated", 10000000000, 4294967295},
	}
	prog, err := parser.Parse(usizeWidthSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	saved := hostPtrBits
	defer func() { hostPtrBits = saved }()
	for _, bits := range []int{64, 32} {
		hostPtrBits = bits
		i := New()
		for _, fn := range prog.Funcs {
			i.Register(fn)
		}
		for _, c := range cases {
			v, err := i.CallByName(c.fn, nil)
			if err != nil {
				t.Fatalf("%d-bit %s: %v", bits, c.fn, err)
			}
			want := c.want64
			if bits == 32 {
				want = c.want32
			}
			n, ok := v.(Number)
			if !ok || uint64(n) != want {
				t.Errorf("%d-bit %s() = %v, want %d", bits, c.fn, v, want)
			}
		}
	}
}
