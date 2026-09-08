package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- `.with` on a string[] must not leak the receiver (#6407) ------
//
// On the native compiler, `arr.with(i, v)` did no rc bookkeeping at all for a
// `string[]`: strings sat outside the counted-array-element set, so the CoW
// copy shared the receiver's element buffers uncounted, the overwritten
// element was never released, and the escape analysis — which keys the
// receiver's reclaimability on that store being counted — tainted the whole
// receiver out of freeEligible. One `.with` stranded N+1 blocks per round.
//
// The self-host compiler lowers `.with` differently — an in-place arr_set on a
// sole-owned slot, a clone plus arr_set on an aliased one, never
// __fern_arr_cow_inplace — but it inherited the leak by another route: the
// in-place store dropped the overwritten element pointer, and the rebind cost
// the array its element-reclaim credit ("SARR:"), so every element box leaked.
// That was invisible while `mks()` denied the control the same credit — both
// columns leaked 380 B/round and the delta was 0. lower_strarr_with_store
// closed it: the store releases the superseded box and retains the value, and
// both columns are now flat.
//
// It is the delta that this asserts, not a byte count. Whatever the self-host
// leaks for unrelated reasons appears in both columns and cancels, so this
// cannot become a byte budget for the rest of the goal-2 port — while still
// failing the moment a `.with` starts costing a per-round block.
func strArrayWithChurnSrc(rounds int, with bool) string {
	set := ""
	if with {
		set = "a = a.with(3, a[5]);"
	}
	return fmt.Sprintf(`import "std/i32";
import "std/string";

function mks(): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 8) { out = out.append("kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; }
    return out;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < %d) {
        var a: string[] = mks();
        %s
        t = t + a.len() + a[3].len();
        r = r + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return t %% 7;
}`, rounds, set)
}

func TestSelfHostStrArrayWithReclaimX86_64(t *testing.T) {
	testSelfHostStrArrayWithReclaim(t, "x86-64-linux")
}

func TestSelfHostStrArrayWithReclaimArm64(t *testing.T) {
	testSelfHostStrArrayWithReclaim(t, "arm64-linux")
}

func testSelfHostStrArrayWithReclaim(t *testing.T, target string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	liveOf := func(t *testing.T, name string, rounds int, update string) int64 {
		t.Helper()
		src := strings.Replace(strArrayWithChurnSrc(rounds, true), "a = a.with(3, a[5]);", update, 1)
		asm := runCaptureEnv(t, runner, driverBin, []byte(src), []string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", target)
		var cmd *exec.Cmd
		if target == "arm64-linux" {
			armgcc, qemu := arm64Tooling(t)
			bin := buildBinArm64(t, armgcc, dir, name, string(asm))
			cmd = runArm64Bin(qemu, bin)
		} else {
			bin := buildBin(t, gcc, dir, name, string(asm))
			cmd = runX86_64Bin(runner, bin)
		}
		out, err := cmd.CombinedOutput()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally: %v\n%s", name, err, out)
		}
		stderr, code := string(out), cmd.ProcessState.ExitCode()
		// Eight elements and a 21-byte string are observed per round. All
		// replacements preserve that length; 99 reports any RC underflow.
		if want := rounds * (8 + 21) % 7; code != want {
			t.Fatalf("%s: exit %d, want %d (88 = wrong value, 99 = RC underflow): %s", name, code, want, stderr)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("parse %q: %v", summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s: allocated nothing — the probe is not exercising the path", name)
		}
		t.Logf("%s (rounds=%d): %s", name, rounds, summary)
		return live
	}

	// The `.with` DELTA against the identical loop without it, at two round
	// counts. Subtracting the control is what keeps this from becoming a byte
	// budget for the rest of the goal-2 port: whatever the self-host leaks
	// for unrelated reasons appears in both columns and cancels.
	base100 := liveOf(t, "strbase100", 100, "")
	base200 := liveOf(t, "strbase200", 200, "")
	for _, tc := range []struct{ name, update string }{
		{"other-element", `a = a.with(3, a[5]); if (a[3] != "kkkkkkkkkkkkkkkkkkkk5") { return 88; }`},
		{"same-element", `a = a.with(3, a[3]); if (a[3] != "kkkkkkkkkkkkkkkkkkkk3") { return 88; }`},
		{"fresh-element", `a = a.with(3, "mmmmmmmmmmmmmmmmmmmm" + "x"); if (a[3] != "mmmmmmmmmmmmmmmmmmmmx") { return 88; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			with100 := liveOf(t, tc.name+"100", 100, tc.update)
			with200 := liveOf(t, tc.name+"200", 200, tc.update)
			d100, d200 := with100-base100, with200-base200
			t.Logf(".with delta: 100 rounds = %d B, 200 rounds = %d B", d100, d200)
			if d200 != d100 {
				t.Errorf(".with on a string[] leaks per round: delta over the same loop without it "+
					"is %d B at 100 rounds and %d B at 200 (control %d / %d). A build-`.with`-discard "+
					"round keeps nothing, so the delta must be flat; growth here is unbounded (#6407)",
					d100, d200, base100, base200)
			}
		})
	}
}

func TestSelfHostStrArrayWithReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	src := strings.Replace(strArrayWithChurnSrc(32, true), "a = a.with(3, a[5]);", `a = a.with(3, a[3]); if (a[3] != "kkkkkkkkkkkkkkkkkkkk3") { return 88; }`, 1)
	src = strings.Replace(src, "function main(): i32", "function churn(): i32", 1)
	src += `
function main(): i32 {
    if (churn() != 4) { return 88; }
    var before: i32 = __heap_bump_bytes() as i32;
    if (churn() != 4) { return 88; }
    var after: i32 = __heap_bump_bytes() as i32;
    if (__rc_underflow() != 0) { return 99; }
    if (after != before) { return 98; }
    return 0;
}`
	wat := runCapture(t, gcc, runner, driver, []byte(src), "-ir")
	path := filepath.Join(dir, "self-store.wat")
	if err := os.WriteFile(path, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("wasmtime", "run", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wasm self-store: %v (88 = wrong value, 98 = heap growth, 99 = RC underflow)\n%s", err, out)
	}
}
