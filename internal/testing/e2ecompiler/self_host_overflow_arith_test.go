package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overflowArithSrc returns the count of saturating and checked results on
// an edge grid that differ from a reference built on plain arithmetic.
const overflowArithSrc = `@noinline function add_lost_i64(a: i64, b: i64, sub: boolean): boolean {
  let big: i64 = 9223372036854775807;
  let small: i64 = 0 - big - 1;
  if (sub) {
    return b < 0 && a > big + b || b > 0 && a < small + b;
  }
  return b > 0 && a > big - b || b < 0 && a < small - b;
}
@noinline function sat_add_i64(a: i64, b: i64): i64 {
  return a +| b;
}
@noinline function sat_sub_i64(a: i64, b: i64): i64 {
  return a -| b;
}
@noinline function chk_add_i64(a: i64, b: i64): Option[i64] {
  return a +? b;
}
@noinline function chk_sub_i64(a: i64, b: i64): Option[i64] {
  return a -? b;
}
@noinline function sat_add_i32(a: i32, b: i32): i32 {
  return a +| b;
}
@noinline function sat_sub_i32(a: i32, b: i32): i32 {
  return a -| b;
}
@noinline function sat_mul_i32(a: i32, b: i32): i32 {
  return a *| b;
}
@noinline function chk_add_i32(a: i32, b: i32): Option[i32] {
  return a +? b;
}
@noinline function chk_sub_i32(a: i32, b: i32): Option[i32] {
  return a -? b;
}
@noinline function chk_mul_i32(a: i32, b: i32): Option[i32] {
  return a *? b;
}
function clamp32(v: i64): (i32, boolean) {
  if (v > 2147483647i64) {
    return (2147483647, true);
  }
  if (v < 0i64 - 2147483648i64) {
    return (0 - 2147483647 - 1, true);
  }
  return (v as i32, false);
}
function opt_bad32(o: Option[i32], want: (i32, boolean)): i32 {
  match (o) {
    Some(v) => {
      if (want.1 || v != want.0) {
        return 1;
      }
    },
    None => {
      if (!want.1) {
        return 1;
      }
    }
  }
  return 0;
}
function opt_bad64(o: Option[i64], lost: boolean, v0: i64): i32 {
  match (o) {
    Some(v) => {
      if (lost || v != v0) {
        return 1;
      }
    },
    None => {
      if (!lost) {
        return 1;
      }
    }
  }
  return 0;
}
function add_bad(g: i64[]): i32 {
  let big: i64 = 9223372036854775807;
  let small: i64 = 0 - big - 1;
  let bad: i32 = 0;
  for a in g {
    for b in g {
      let lost: boolean = add_lost_i64(a, b, false);
      let want: i64 = a + b;
      if (lost) {
        want = big;
        if (a < 0) {
          want = small;
        }
      }
      if (sat_add_i64(a, b) != want) {
        bad = bad + 1;
      }
      bad = bad + opt_bad64(chk_add_i64(a, b), lost, a + b);
      lost = add_lost_i64(a, b, true);
      want = a - b;
      if (lost) {
        want = big;
        if (a < 0) {
          want = small;
        }
      }
      if (sat_sub_i64(a, b) != want) {
        bad = bad + 1;
      }
      bad = bad + opt_bad64(chk_sub_i64(a, b), lost, a - b);
    }
  }
  let n: i32[] = [0, 1, 0 - 1, 2, 0 - 2, 46340, 46341, 0 - 46341, 65536, 0 - 65536, 1073741824, 0 - 1073741824, 2147483647, 0 - 2147483647 - 1, 2147483646, 0 - 2147483647];
  for a in n {
    for b in n {
      let wa: i64 = a as i64;
      let wb: i64 = b as i64;
      let s: (i32, boolean) = clamp32(wa + wb);
      let d: (i32, boolean) = clamp32(wa - wb);
      let m: (i32, boolean) = clamp32(wa * wb);
      if (sat_add_i32(a, b) != s.0 || sat_sub_i32(a, b) != d.0 || sat_mul_i32(a, b) != m.0) {
        bad = bad + 1;
      }
      bad = bad + opt_bad32(chk_add_i32(a, b), s) + opt_bad32(chk_sub_i32(a, b), d) + opt_bad32(chk_mul_i32(a, b), m);
    }
  }
  return bad;
}
function grid(): i64[] {
  let big: i64 = 9223372036854775807;
  let small: i64 = 0 - big - 1;
  return [0, 1, 0 - 1, 2, 0 - 2, 3, 3037000499, 3037000500, 0 - 3037000499, 0 - 3037000500, 4294967296, 0 - 4294967296, 4611686018427387904, 0 - 4611686018427387904, big, small, big / 2, small / 2, big - 1, small + 1];
}
@noinline function lost_i64(a: i64, b: i64): boolean {
  let big: i64 = 9223372036854775807;
  let small: i64 = 0 - big - 1;
  if (a == 0) {
    return false;
  }
  if (a == 0 - 1) {
    return b == small;
  }
  return a * b / a != b;
}
@noinline function sat_i64(a: i64, b: i64): i64 {
  return a *| b;
}
@noinline function chk_i64(a: i64, b: i64): Option[i64] {
  return a *? b;
}
@noinline function sat_u64(a: u64, b: u64): u64 {
  return a *| b;
}
@noinline function chk_u64(a: u64, b: u64): Option[u64] {
  return a *? b;
}
function main(): i32 {
  let big: i64 = 9223372036854775807;
  let small: i64 = 0 - big - 1;
  let bad: i32 = 0;
  let g: i64[] = grid();
  for a in g {
    for b in g {
      let lost: boolean = lost_i64(a, b);
      let want: i64 = a * b;
      if (lost) {
        want = big;
        if (a < 0 != b < 0) {
          want = small;
        }
      }
      if (sat_i64(a, b) != want) {
        bad = bad + 1;
      }
      match (chk_i64(a, b)) {
        Some(v) => {
          if (lost || v != a * b) {
            bad = bad + 1;
          }
        },
        None => {
          if (!lost) {
            bad = bad + 1;
          }
        }
      }
      let ua: u64 = a as u64;
      let ub: u64 = b as u64;
      let ulost: boolean = ua != 0 as u64 && ub > 18446744073709551615u64 / ua;
      let uwant: u64 = ua * ub;
      if (ulost) {
        uwant = 18446744073709551615u64;
      }
      if (sat_u64(ua, ub) != uwant) {
        bad = bad + 1;
      }
      match (chk_u64(ua, ub)) {
        Some(v) => {
          if (ulost || v != ua * ub) {
            bad = bad + 1;
          }
        },
        None => {
          if (!ulost) {
            bad = bad + 1;
          }
        }
      }
    }
  }
  return bad + add_bad(g);
}
`

// TestSelfHostOverflowArith checks the saturating and checked add, subtract
// and multiply on every target against the interpreter. On the native
// targets, the 64-bit multiplies must not divide.
func TestSelfHostOverflowArith(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	cli := buildSelfHostCLI(t)
	if want := interpExit(t, interpBin, overflowArithSrc); want != 0 {
		t.Fatalf("interpreter: %d results differ from the reference", want)
	}
	path := filepath.Join(t.TempDir(), "overflow.fern")
	if err := os.WriteFile(path, []byte(overflowArithSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	divides := map[string][]string{"x86-64-linux": {"idiv", "\tdiv", " div"}, "arm64-linux": {"sdiv", "udiv"}}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, overflowArithSrc, target, "FERN_STRICT_IR=1"); code != 0 {
				t.Fatalf("exit %d: results differ from the reference\n%s", code, stderr)
			}
			if divides[target] == nil {
				return
			}
			asm, err := os.ReadFile(cli.emit(t, path, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, fn := range []string{"sat_i64", "chk_i64", "sat_u64", "chk_u64"} {
				body := asmFunction(string(asm), "__fn_"+fn+":")
				if body == "" {
					t.Fatalf("no %s in the assembly", fn)
				}
				for _, d := range divides[target] {
					if strings.Contains(body, d) {
						t.Errorf("%s divides (%q):\n%s", fn, d, body)
					}
				}
			}
		})
	}
}

func asmFunction(asm, label string) string {
	at := strings.Index(asm, "\n"+label)
	if at < 0 {
		return ""
	}
	body := asm[at+1:]
	if end := strings.Index(body, ".cfi_endproc"); end >= 0 {
		body = body[:end]
	}
	return body
}
