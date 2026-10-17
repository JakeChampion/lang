package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A -size build calls the reference-count helpers rather than testing the
// count inline (asm_ir / asm_arm64_ir ssa_call): `g` releases the string it
// built through __fern_str_free's register entry, with none of the inline
// guard's labels, and `h` retains the array it goes on reading through
// __fern_rc_inc, reading it through a call semhoist cannot move ahead of the
// append. The leaf `mix` is spliced into `k` by default and called
// under -size, which splices only what is called once (seminline); so is
// `wrap`, a leaf only once `mix2`, called from it alone, folds in. The
// program answers the same either way, -size drops the
// backtrace walk unless -backtrace asks for it, and the net-bench hello
// server, std/serve whole, builds smaller.
const sizeModeProg = `@noinline function g(a: string): i32 { let t: string = a + "x"; return t.len(); }
@noinline function size(xs: i32[]): i32 { return xs.len(); }
@noinline function h(xs: i32[]): i32 { let ys: i32[] = xs; let zs: i32[] = ys.append(1); return zs.len() + size(xs); }
function mix(x: i32): i32 {
  let a: i32 = x * 31 + 7;
  let b: i32 = (a ^ (a >> 3)) * 17;
  let c: i32 = (b + x) % 1009;
  return c * c + a - b;
}
@noinline function k(a: i32, b: i32): i32 { return mix(a) + mix(b); }
function mix2(x: i32): i32 {
  let a: i32 = x * 29 + 3;
  let b: i32 = (a ^ (a >> 2)) * 13;
  let c: i32 = (b + x) % 1013;
  return c * c + a - b;
}
function wrap(x: i32): i32 { return mix2(x) + 1; }
@noinline function w(a: i32, b: i32): i32 { return wrap(a) + wrap(b); }
function main(): i32 { return g("abc" + "") + g("") * 10 + h([1, 2, 3]) * 100 + k(args().len(), 2) + w(args().len(), 3) - 5; }
`

func TestSelfHostSizeMode(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "size.fern")
	if err := os.WriteFile(src, []byte(sizeModeProg), 0o644); err != nil {
		t.Fatal(err)
	}
	emit := func(target string, flags ...string) string {
		t.Helper()
		out := filepath.Join(dir, target+strings.Join(flags, "")+".s")
		args := append(append([]string{}, flags...), "-target", target, "-emit", "asm", "-o", out, src, h.stdlib)
		if combined, err := exec.Command(h.cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: emitting: %v\n%s", target, flags, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(asm)
	}
	release := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`call __fn___fern_str_free\.r\n`),
		"arm64-linux":  regexp.MustCompile(`bl __fn___fern_str_free\.r\n`),
	}
	retain := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`call __fn___fern_rc_inc\n`),
		"arm64-linux":  regexp.MustCompile(`bl __fn___fern_rc_inc\n`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		asm := emit(target, "-size")
		g := condBranchBody(t, asm, "g")
		if strings.Contains(g, "rcdec") || !release[target].MatchString(g) {
			t.Errorf("%s -size: g does not release its string through the helper's register entry alone:\n%s", target, g)
		}
		if hb := condBranchBody(t, asm, "h"); strings.Contains(hb, "rcinc") || !retain[target].MatchString(hb) {
			t.Errorf("%s -size: h does not retain its array through __fern_rc_inc:\n%s", target, hb)
		}
		mixCall := regexp.MustCompile(`(call|bl) __fn_mix\b`)
		if kb := condBranchBody(t, asm, "k"); !mixCall.MatchString(kb) {
			t.Errorf("%s -size: k splices the leaf mix rather than calling it:\n%s", target, kb)
		}
		def := emit(target)
		if kb := condBranchBody(t, def, "k"); mixCall.MatchString(kb) {
			t.Errorf("%s: by default k calls the leaf mix rather than splicing it:\n%s", target, kb)
		}
		wrapCall := regexp.MustCompile(`(call|bl) __fn_wrap\b`)
		if wb := condBranchBody(t, asm, "w"); !wrapCall.MatchString(wb) {
			t.Errorf("%s -size: w splices wrap, grown into a leaf, rather than calling it:\n%s", target, wb)
		}
		if wb := condBranchBody(t, def, "w"); wrapCall.MatchString(wb) {
			t.Errorf("%s: by default w calls wrap, grown into a leaf, rather than splicing it:\n%s", target, wb)
		}
		if strings.Contains(asm, "backtrace:") {
			t.Errorf("%s -size: the backtrace walk is still emitted", target)
		}
		if !strings.Contains(emit(target, "-size", "-backtrace"), "backtrace:") {
			t.Errorf("%s -size -backtrace: the backtrace walk is not emitted", target)
		}
	}
	for _, tg := range h.targets {
		for _, mode := range []string{"default", "size"} {
			bin := filepath.Join(dir, tg.target+"."+mode)
			args := []string{"-target", tg.target, "-o", bin, src, h.stdlib}
			if mode == "size" {
				args = append([]string{"-size"}, args...)
			}
			if combined, err := exec.Command(h.cli, args...).CombinedOutput(); err != nil {
				t.Fatalf("%s %s: building: %v\n%s", tg.target, mode, err, combined)
			}
			run := exec.Command(bin)
			if len(tg.runner) > 0 {
				run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
			}
			_ = run.Run()
			if got := run.ProcessState.ExitCode(); got != sizeModeExit() {
				t.Errorf("%s %s: exit %d, want %d", tg.target, mode, got, sizeModeExit())
			}
		}
	}
	hello := filepath.Join(repoRootFromTest(t), "scripts", "net-bench.d", "hello.fern")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		sizes := map[string]int64{}
		for _, mode := range []string{"default", "size"} {
			bin := filepath.Join(dir, target+".hello."+mode)
			args := []string{"-target", target, "-o", bin, hello, h.stdlib}
			if mode == "size" {
				args = append([]string{"-size"}, args...)
			}
			if combined, err := exec.Command(h.cli, args...).CombinedOutput(); err != nil {
				t.Fatalf("%s %s: building the hello server: %v\n%s", target, mode, err, combined)
			}
			fi, err := os.Stat(bin)
			if err != nil {
				t.Fatal(err)
			}
			sizes[mode] = fi.Size()
		}
		if sizes["size"] >= sizes["default"]*9/10 {
			t.Errorf("%s: the -size hello server is %d bytes, the default %d: not a tenth smaller", target, sizes["size"], sizes["default"])
		}
		t.Logf("%s hello server: %d bytes, %d with -size", target, sizes["default"], sizes["size"])
	}
}

// sizeModeExit is what sizeModeProg's main answers, as an exit status: g
// gives 14, h 700, k(1, 2) the two mixes and w(1, 3) the two wraps, less 5.
func sizeModeExit() int {
	mix := func(x int32) int32 {
		a := x*31 + 7
		b := (a ^ (a >> 3)) * 17
		c := (b + x) % 1009
		return c*c + a - b
	}
	mix2 := func(x int32) int32 {
		a := x*29 + 3
		b := (a ^ (a >> 2)) * 13
		c := (b + x) % 1013
		return c*c + a - b
	}
	return int(uint8(14 + 700 + mix(1) + mix(2) + mix2(1) + 1 + mix2(3) + 1 - 5))
}
