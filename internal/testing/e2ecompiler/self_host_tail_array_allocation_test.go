package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const tailArrayAllocationSource = `@noinline function raw_arr_box(x: f64): f64 { return x + 1.0; }
@noinline function lane_keep(v: f64x2): f64x2 { return v + __f64x2_splat(2.0); }
@noinline function lane_tail(v: f64x2): f64x2 { return lane_keep(v); }
@noinline function overwrite(p: usize): i32 { __store_i32(p, 7); return 0; }
@noinline function live(n: i32): i32[] {
  overwrite(__raw_scratch(8));
  return __raw_array(__raw_arr_box(n));
}
@noinline function fresh(n: i32): i32[] {
  return __raw_array(__raw_arr_box(n));
}
@noinline function many(a: i64, b: i64, c: i64, d: i64, e: i64, f: i64,
                       g: i64, h: i64, i: i64, j: i64, k: i64, l: i64, n: i32): i32[] {
  return __raw_array(__raw_arr_box(((a+b+c+d+e+f+g+h+i+j+k+l) as i32) + n));
}
@noinline function cleanup(own old: i32[], n: i32): i32[] {
  let p: usize = __raw_arr_box(n);
  __store_i64(p + 8, old[0] as i64);
  return __raw_array(p);
}
@noinline function checked_cleanup(n: i32): i32 {
  let c: i32[] = cleanup([n], 1);
  return c[0];
}
function main(): i32 {
  let a: i32[] = fresh(3);
  let b: i32[] = many(1i64,2i64,3i64,4i64,5i64,6i64,7i64,8i64,9i64,10i64,11i64,12i64,2);
  let c: i32 = checked_cleanup(17);
  let d: i32[] = live(5);
  if (a.len() != 3 || b.len() != 80 || c != 17 || d.len() != 5 || raw_arr_box(1.25) != 2.25) { return 91; }
  if (__f64x2_lane(lane_tail(__f64x2_splat(0.5)), 1) != 2.5) { return 92; }
  return 0;
}
`

func TestSelfHostTailArrayAllocation(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "allocation.fern")
	if err := os.WriteFile(src, []byte(tailArrayAllocationSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			modes := []string{"plain", "sanitized"}
			if target == "arm64-linux" {
				modes = append(modes, "hardened")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					var env []string
					if mode == "sanitized" {
						env = []string{"FERN_SANITIZE=1"}
					}
					asmPath := cli.emit(t, src, target, env...)
					if mode == "hardened" {
						cmd := runX86_64Bin(cli.runner, cli.bin, "-harden", "-target", target, "-emit", "asm", "-o", asmPath, src, cli.stdlib)
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("hardened emit: %v\n%s", err, out)
						}
					}
					raw, err := os.ReadFile(asmPath)
					if err != nil {
						t.Fatal(err)
					}
					tail, call := `\bjmp __fern_arr_box\b`, "call __fern_arr_box"
					if target == "arm64-linux" {
						tail, call = `\bb __fern_arr_box\b`, "bl __fern_arr_box"
					}
					for _, name := range []string{"fresh", "many", "live"} {
						body := asmFuncBody(t, string(raw), "__fn_"+name)
						if !regexp.MustCompile(tail).MatchString(body) || strings.Contains(body, call) {
							t.Errorf("%s does not tail-allocate:\n%s", name, body)
						}
					}
					if body := asmFuncBody(t, string(raw), "__fn_cleanup"); !strings.Contains(body, call) {
						t.Errorf("post-allocation work must retain the call:\n%s", body)
					}
					laneTail := `\bjmp __fn_lane_keep\.r\b`
					if target == "arm64-linux" {
						laneTail = `\bb __fn_lane_keep\.r\b`
					}
					if body := asmFuncBody(t, string(raw), "__fn_lane_tail"); !regexp.MustCompile(laneTail).MatchString(body) {
						t.Errorf("lane arguments must keep their register ABI in a tail call:\n%s", body)
					}
					gcc, runner := x86_64Tooling(t)
					if target == "arm64-linux" {
						var qemu string
						gcc, qemu = arm64Tooling(t)
						runner = nil
						if qemu != "" {
							runner = []string{qemu}
						}
					}
					bin := buildBin(t, gcc, t.TempDir(), "allocation", string(raw))
					check := func(bin string) {
						t.Helper()
						stderr, code := runCapturingStderr(t, ssaBackendTarget{runner: runner}, bin)
						if code != 0 || strings.Contains(stderr, "fern-sanitizer:") {
							t.Fatalf("exit=%d\n%s", code, stderr)
						}
						if mode == "sanitized" && !strings.Contains(stderr, "allocs=5 frees=5 live_bytes=0") {
							t.Fatalf("cleanup did not balance:\n%s", stderr)
						}
					}
					check(bin)
					native := filepath.Join(t.TempDir(), "native")
					args := []string{"-target", target, "-o", native, src, cli.stdlib}
					if mode == "hardened" {
						args = append([]string{"-harden"}, args...)
					}
					cmd := runX86_64Bin(cli.runner, cli.bin, args...)
					cmd.Env = append(os.Environ(), env...)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("native link: %v\n%s", err, out)
					}
					if err := os.Chmod(native, 0o755); err != nil {
						t.Fatal(err)
					}
					check(native)
				})
			}
		})
	}
}
