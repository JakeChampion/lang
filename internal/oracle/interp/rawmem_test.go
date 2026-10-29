package interp

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const rawMemSrc = `
function byte_zero_extends(): i32 {
  let n: i32 = 0;
  unsafe {
    let p: usize = __alloc(1);
    __store_u8(p, 0 - 1);
    n = __load_u8(p);
    __free(p, 1);
  }
  return n;
}

function word_sign_extends(): i64 {
  let n: i64 = 0;
  unsafe {
    let p: usize = __alloc(8);
    __store_i64(p, 0 - 1);
    n = __load_i32(p) as i64;
    __free(p, 8);
  }
  return n;
}

function pointer_round_trips(): usize {
  let q: usize = 0;
  unsafe {
    let p: usize = __alloc(__ptr_width());
    __store_ptr(p, p);
    q = __load_ptr(p) - p;
    __free(p, __ptr_width());
  }
  return q;
}

function reuse_is_zeroed(): i32 {
  let n: i32 = 0;
  unsafe {
    let p: usize = __alloc(24);
    __memset(p, 255, 24);
    __free(p, 24);
    let q: usize = __alloc(20);
    n = __load_i32(q + 16 as usize);
    __free(q, 20);
  }
  return n;
}

function copy_moves_bytes(): i32 {
  let n: i32 = 0;
  unsafe {
    let p: usize = __alloc(8);
    let q: usize = __alloc(8);
    __store_i32(p + 4 as usize, 70000);
    __memcpy(q, p, 8);
    n = __load_i32(q + 4 as usize);
    __free(q, 8);
    __free(p, 8);
  }
  return n;
}

function double_free(): i32 {
  unsafe {
    let p: usize = __alloc(4);
    __free(p, 4);
    __free(p, 4);
  }
  return 0;
}

function wrong_size_free(): i32 {
  unsafe {
    let p: usize = __alloc(4);
    __free(p, 8);
  }
  return 0;
}

function interior_free(): i32 {
  unsafe {
    let p: usize = __alloc(8);
    __free(p + 4 as usize, 4);
  }
  return 0;
}

function read_past_end(): i32 {
  let n: i32 = 0;
  unsafe {
    let p: usize = __alloc(6);
    n = __load_i32(p + 4 as usize);
  }
  return n;
}

function read_after_free(): i32 {
  let n: i32 = 0;
  unsafe {
    let p: usize = __alloc(4);
    __free(p, 4);
    n = __load_i32(p);
  }
  return n;
}

function read_null(): i32 {
  let n: i32 = 0;
  unsafe {
    n = __load_i32(0 as usize);
  }
  return n;
}

function overlapping_copy(): i32 {
  unsafe {
    let p: usize = __alloc(8);
    __memcpy(p + 2 as usize, p, 4);
  }
  return 0;
}

function main(): i32 {
  return 0;
}
`

func rawMemInterp(t *testing.T) *Interp {
	t.Helper()
	prog, err := parser.Parse(rawMemSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	return i
}

func TestRawMemoryReadsBackWhatItStores(t *testing.T) {
	saved := hostPtrBits
	defer func() { hostPtrBits = saved }()
	for _, bits := range []int{64, 32} {
		hostPtrBits = bits
		i := rawMemInterp(t)
		for _, c := range []struct {
			fn   string
			want int64
		}{
			{"byte_zero_extends", 255},
			{"word_sign_extends", -1},
			{"pointer_round_trips", 0},
			{"reuse_is_zeroed", 0},
			{"copy_moves_bytes", 70000},
		} {
			v, err := i.CallByName(c.fn, nil)
			if err != nil {
				t.Fatalf("%d-bit %s: %v", bits, c.fn, err)
			}
			if n, ok := v.(Number); !ok || int64(n) != c.want {
				t.Errorf("%d-bit %s() = %v, want %d", bits, c.fn, v, c.want)
			}
		}
	}
}

func TestRawMemoryRefusesABrokenObligation(t *testing.T) {
	i := rawMemInterp(t)
	for _, c := range []struct{ fn, want string }{
		{"double_free", "already free"},
		{"wrong_size_free", "has 4 bytes, not 8"},
		{"interior_free", "not the start of a block"},
		{"read_past_end", "run past the end of the 6-byte block"},
		{"read_after_free", "in a freed block"},
		{"read_null", "not in a block from __alloc"},
		{"overlapping_copy", "overlap"},
	} {
		_, err := i.CallByName(c.fn, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", c.fn, err, c.want)
		}
	}
}
