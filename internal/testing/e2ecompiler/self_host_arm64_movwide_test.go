package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const movwideProg = `import "std/u64";

@noinline function k1(): u64 { return 0xffffffffffffffffu64; }
@noinline function k2(): u64 { return 4294967295u64; }
@noinline function k3(): u64 { return 0x7fff000000000000u64; }
@noinline function k4(): u64 { return 0xfffffffffffeffffu64; }
@noinline function k5(): u64 { return 65536u64; }
@noinline function k6(): u64 { return 0x00000000ffff0000u64; }
@noinline function k7(): u64 { return 0xffff0000ffffffffu64; }
@noinline function k8(): u64 { return 0x0000ffff00000000u64; }
@noinline function k9(): u64 { return 0x0000ffffffffffffu64; }
@noinline function k10(): u64 { return 0x00000000fffeffffu64; }
@noinline function k11(): u64 { return 1000000u64; }
@noinline function k12(): i64 { return 0i64 - 65537i64; }
@noinline function k13(): i64 { return 9223372036854775807i64; }
@noinline function k14(): u64 { return 0x8000000000000000u64; }
function main(): i32 {
  let all: u64[] = [k1(), k2(), k3(), k4(), k5(), k6(), k7(), k8(), k9(), k10(), k11(), k12() as u64, k13() as u64, k14()];
  let acc: u64 = 0u64;
  for v in all {
    print(v.to_string());
    acc = acc * 31u64 + v;
  }
  return (acc % 251u64) as i32;
}
`

func TestSelfHostArm64MovWideConstants(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "consts.fern")
	if err := os.WriteFile(src, []byte(movwideProg), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := exec.Command(h.native, "-interp", src)
	want, _ := oracle.Output()
	if oracle.ProcessState == nil || !oracle.ProcessState.Exited() {
		t.Fatalf("the interpreter did not exit normally: %v", oracle.ProcessState)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, "consts-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if got, code := h.runProduced(t, tg, bin); got != string(want) || code != oracle.ProcessState.ExitCode() {
			t.Errorf("%s: printed %q and exited %d, the interpreter %q and %d", tg.target, got, code, want, oracle.ProcessState.ExitCode())
		}
	}
	out := filepath.Join(dir, "consts.s")
	if report, err := exec.Command(h.cli, "-target", "arm64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, report)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	pool := regexp.MustCompile(`(?m)^\s+ldr x\d+, =`)
	wide := regexp.MustCompile(`(?m)^\s+mov[zn] [xw]\d+, #\d+(, lsl #\d+)?$`)
	for _, name := range strings.Fields("k1 k2 k3 k4 k5 k6 k7 k8 k9 k10 k12 k13 k14") {
		fn := functionListing(string(asm), "__fn_"+name)
		if fn == "" {
			t.Fatalf("no __fn_%s in the listing", name)
		}
		if pool.MatchString(fn) || !wide.MatchString(fn) {
			t.Errorf("%s should load its constant with one move-wide:\n%s", name, fn)
		}
	}
	if fn := functionListing(string(asm), "__fn_k11"); !pool.MatchString(fn) {
		t.Errorf("k11 needs two halfwords and should stay in the pool:\n%s", fn)
	}
}
