package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const perModuleCounterLibSrc = `@noinline pub function probe(): i32 {
    let xs: i32[] = [];
    let i: i32 = 1;
    while (i <= 3) { xs = xs.append(i); i = i + 1; }
    let mark: i64 = __heap_bump_bytes();
    if (mark <= (0 as i64)) { return 1; }
    if (xs.len() != 3) { return 2; }
    if (__arr_push_shared_bytes() != (0 as i64)) { return 3; }
    return 42 + __arr_push_shared_count();
}
`

const perModuleCounterMainSrc = `import "./counters";
function main(): i32 { return counters.probe(); }
`

func writePerModuleCounterProject(t *testing.T) string {
	t.Helper()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "counters.fern"), []byte(perModuleCounterLibSrc), 0o644); err != nil {
		t.Fatalf("write counters.fern: %v", err)
	}
	entry := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(entry, []byte(perModuleCounterMainSrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return entry
}

func perModuleUnits(t *testing.T, runner []string, driverBin, entry string, targetArgs []string, minModules int) []string {
	t.Helper()
	drive := func(args ...string) string {
		t.Helper()
		full := append(append([]string{entry}, targetArgs...), args...)
		out, err := runX86_64Bin(runner, driverBin, full...).Output()
		if err != nil {
			t.Fatalf("driver %v: %v", args, err)
		}
		return string(out)
	}

	countOut := drive("-per-module-count")
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < minModules {
		t.Fatalf("-per-module-count = %q (n=%d err=%v), want >= %d", countOut, n, err, minModules)
	}

	var needArgs []string
	for _, ln := range strings.Split(drive("-per-module-needs"), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	dir := filepath.Dir(entry)
	var objs []string
	sawLibRef := map[string]bool{
		"__fern_heap_ptr":        false,
		"__fern_arr_push_shared": false,
		"__fern_arr_push_copied": false,
	}
	for i := 0; i < n; i++ {
		unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
		if len(unit) == 0 {
			t.Fatalf("module %d emitted 0 bytes", i)
		}
		if !strings.Contains(unit, "_start:") {
			for sym := range sawLibRef {
				if strings.Contains(unit, sym) {
					sawLibRef[sym] = true
				}
			}
		}
		p := filepath.Join(dir, "cu"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		objs = append(objs, p)
	}
	for _, sym := range []string{"__fern_heap_ptr", "__fern_arr_push_shared", "__fern_arr_push_copied"} {
		if !sawLibRef[sym] {
			t.Fatalf("no library unit referenced %s — the test no longer drives the cross-unit shape for it", sym)
		}
	}
	return objs
}

func TestSelfHostPerModuleCounterBSSLinkX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "counterbss")

	entry := writePerModuleCounterProject(t)
	objs := perModuleUnits(t, runner, driverBin, entry, nil, 2)

	bin := filepath.Join(filepath.Dir(entry), "prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("per-module link failed — a counter's .bss word is not .globl (#6058 class):\n%v\n%s", err, lout)
	}

	var rcmd *exec.Cmd
	if len(runner) == 0 {
		rcmd = exec.Command(bin)
	} else {
		rcmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = rcmd.Run()
	if code := rcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("cross-unit counter program exited %d, want 42", code)
	}
}

func TestSelfHostPerModuleCounterBSSLinkArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "counterbssarm64")

	entry := writePerModuleCounterProject(t)
	objs := perModuleUnits(t, x86runner, driverBin, entry, []string{"-target", "arm64-linux"}, 2)

	bin := filepath.Join(filepath.Dir(entry), "prog")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
	if lout, err := exec.Command(armgcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("per-module arm64 link failed — a counter's .bss word is not .globl (#6058 class):\n%v\n%s", err, lout)
	}
	rcmd := runArm64Bin(qemu, bin)
	_ = rcmd.Run()
	if code := rcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("cross-unit counter program exited %d, want 42 (arm64)", code)
	}
}
