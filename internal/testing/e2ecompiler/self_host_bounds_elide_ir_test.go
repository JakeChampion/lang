package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var boundsElideCases = []struct {
	name string
	main string
}{
	{"while_sum", `function main(): i32 {
    let xs: i32[] = [3, 5, 7, 11, 13];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s;
}`},
	{"while_step_two", `function main(): i32 {
    let xs: i32[] = [1, 100, 2, 200, 3];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 2; }
    return s;
}`},
	{"nested_if_access", `function main(): i32 {
    let xs: i32[] = [4, 9, 2, 7, 6, 1];
    let e: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { if (xs[i] % 2 == 0) { e = e + xs[i]; } i = i + 1; }
    return e;
}`},
	{"two_loops", `function main(): i32 {
    let xs: i32[] = [2, 3, 4];
    let ys: i32[] = [10, 20, 30, 40];
    let a: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { a = a + xs[i]; i = i + 1; }
    let j: i32 = 0;
    while (j < ys.len()) { a = a + ys[j]; j = j + 1; }
    return a;
}`},
	{"i64_elems", `function main(): i32 {
    let xs: i64[] = [10, 20, 30, 40];
    let s: i64 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
    return s as i32;
}`},
	{"arr_reassigned_not_elided", `function main(): i32 {
    let xs: i32[] = [5, 6, 7];
    let ys: i32[] = [9];
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { s = s + xs[i]; xs = ys; i = i + 1; }
    return s;
}`},
	{"at_binder_shadow_not_elided", `enum W { One(i32), Two(i32) }
function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) {
        match (W.One(t)) {
            i @ W.One(v) => { t = t + v; },
            _ => {},
        }
        t = t + xs[i];
        i = i + 1;
    }
    return t;
}`},
	{"destructure_shadow_not_elided", `function pair(): (i32, i32) { return (0, 9); }
function main(): i32 {
    let xs: i32[] = [1, 2, 3, 4];
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) {
        if (t > 100) { let (i, y) = pair(); t = t + i + y; }
        t = t + xs[i];
        i = i + 1;
    }
    return t;
}`},
	{"field_path", `struct R { xs: i32[] }
function main(): i32 {
    let r: R = R { xs: [3, 5, 7] };
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < r.xs.len()) { s = s + r.xs[i]; i = i + 1; }
    return s;
}`},
	{"cached_len", `function main(): i32 {
    let xs: i32[] = [3, 5, 7, 11];
    let i: i32 = 0;
    let n: i32 = xs.len();
    let s: i32 = 0;
    while (i < n) { s = s + xs[i]; i = i + 1; }
    return s;
}`},
	{"cached_str_len", `function main(): i32 {
    let t: string = "abc";
    let n: i32 = t.len();
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < n) { s = s + (t[i] as i32); i = i + 1; }
    return s % 256;
}`},
	{"conjunct_guard", `function main(): i32 {
    let xs: i32[] = [4, 9, 2, 0, 6];
    let i: i32 = 0;
    while (i < xs.len() && xs[i] != 0) { i = i + 1; }
    return i;
}`},
	{"field_root_reassigned_not_elided", `struct R { xs: i32[] }
function main(): i32 {
    let r: R = R { xs: [3, 5, 7] };
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < r.xs.len()) { s = s + r.xs[i]; r = R { xs: [1] }; i = i + 1; }
    return s;
}`},
}

func TestSelfHostBoundsElideIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	emit := func(src string) string {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		asm, err := cmd.Output()
		if err != nil || len(asm) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		return string(asm)
	}
	runExit := func(t *testing.T, bin string) int {
		var run *exec.Cmd
		if len(runner) == 0 {
			run = exec.Command(bin)
		} else {
			run = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = run.Run()
		return run.ProcessState.ExitCode()
	}

	for _, tc := range boundsElideCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			asm := emit(src)
			if !strings.Contains(asm, ".Lssa_") {
				t.Fatalf("%s: did not lower through the IR (no .Lssa_ labels)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "belide_"+tc.name, asm)
			if code := runExit(t, bin); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}

	noElide := `function main(): i32 { let xs: i32[] = [3, 5, 7, 11, 13]; let n: i32 = 5; let s: i32 = 0; let i: i32 = 0; while (i < n) { s = s + xs[i]; i = i + 1; } return s; }`
	for _, d := range []struct{ name, src string }{
		{"for_in", `function main(): i32 { let xs: i32[] = [3, 5, 7, 11, 13]; let s: i32 = 0; for x in xs { s = s + x; } return s; }`},
		{"len_guard", `function main(): i32 { let xs: i32[] = [3, 5, 7, 11, 13]; let s: i32 = 0; let i: i32 = 0; while (i < xs.len()) { s = s + xs[i]; i = i + 1; } return s; }`},
		{"cached_len", `function main(): i32 { let xs: i32[] = [3, 5, 7, 11, 13]; let n: i32 = xs.len(); let s: i32 = 0; let i: i32 = 0; while (i < n) { s = s + xs[i]; i = i + 1; } return s; }`},
		{"field_path", `struct R { xs: i32[] } function main(): i32 { let r: R = R { xs: [3, 5, 7, 11, 13] }; let s: i32 = 0; let i: i32 = 0; while (i < r.xs.len()) { s = s + r.xs[i]; i = i + 1; } return s; }`},
		{"conjunct", `function main(): i32 { let xs: i32[] = [3, 5, 7, 11, 13]; let s: i32 = 0; let i: i32 = 0; while (i < xs.len() && s < 100) { s = s + xs[i]; i = i + 1; } return s; }`},
		{"cond_read_after_guard", `function main(): i32 { let xs: i32[] = [3, 5, 7, 11, 13]; let i: i32 = 0; while (i < xs.len() && xs[i] > 0) { i = i + 1; } return i + 34; }`},
	} {
		t.Run("elision_fired_differential/"+d.name, func(t *testing.T) {
			got := strings.Count(emit(d.src), "__fern_oob_abort")
			base := strings.Count(emit(noElide), "__fern_oob_abort")
			if got >= base {
				t.Fatalf("elision did not fire: elidable emitted %d __fern_oob_abort, non-elidable %d (want fewer)", got, base)
			}
			for name, prog := range map[string]string{"elide": d.src, "noElide": noElide} {
				if code := runExit(t, buildBin(t, gcc, dir, "beldiff_"+d.name+"_"+name, emit(prog))); code != 39 {
					t.Errorf("%s exited %d, want 39", name, code)
				}
			}
		})
	}

	for _, c := range []struct{ name, src string }{
		{"cached_len_arr_reassigned", `function main(): i32 { let xs: i32[] = [1, 2, 3]; let n: i32 = xs.len(); let s: i32 = 0; let i: i32 = 0; while (i < n) { s = s + xs[i]; xs = [9]; i = i + 1; } return s; }`},
		{"cached_len_arr_reassigned_before", `function main(): i32 { let xs: i32[] = [1, 2, 3]; let n: i32 = xs.len(); xs = [9]; let s: i32 = 0; let i: i32 = 0; while (i < n) { s = s + xs[i]; i = i + 1; } return s; }`},
		{"cached_len_reassigned", `function main(): i32 { let xs: i32[] = [1, 2, 3]; let n: i32 = xs.len(); n = 4; let s: i32 = 0; let i: i32 = 0; while (i < n) { s = s + xs[i]; i = i + 1; } return s; }`},
		{"field_root_reassigned_before_read", `struct R { xs: i32[] } function main(): i32 { let r: R = R { xs: [1, 2, 3] }; let s: i32 = 0; let i: i32 = 0; while (i < r.xs.len()) { r = R { xs: [] }; s = s + r.xs[i]; i = i + 1; } return s; }`},
		{"index_reset_between", `function main(): i32 { let xs: i32[] = [1, 2, 3]; let i: i32 = 0; let n: i32 = xs.len(); i = 0 - 1; let s: i32 = 0; while (i < n) { s = s + xs[i]; i = i + 1; } return s; }`},
		{"cond_read_before_guard", `function main(): i32 { let xs: i32[] = [1, 2, 3]; let i: i32 = 0; while (xs[i] > 0 && i < xs.len()) { i = i + 1; } return i; }`},
	} {
		t.Run("stays_checked/"+c.name, func(t *testing.T) {
			bin := buildBin(t, gcc, dir, "belide_"+c.name, emit(c.src))
			if code := runExit(t, bin); code != 134 {
				t.Errorf("%s exited %d, want 134 (bounds check must remain)", c.name, code)
			}
		})
	}

	t.Run("read_after_increment_stays_checked", func(t *testing.T) {
		src := `function main(): i32 { let xs: i32[] = [1, 2, 3]; let s: i32 = 0; let i: i32 = 0; while (i < xs.len()) { i = i + 1; s = s + xs[i]; } return s; }`
		bin := buildBin(t, gcc, dir, "belide_after_incr", emit(src))
		if code := runExit(t, bin); code != 134 {
			t.Errorf("read-after-increment exited %d, want 134 (bounds check must remain)", code)
		}
	})
}

func TestSelfHostBoundsElideIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host bounds-elide wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range boundsElideCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = strings.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "belide_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("bounds-elide wasm IR %q = %d, want %d", tc.name, got, want)
			}
		})
	}
}

func TestSelfHostBoundsElideIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range boundsElideCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			progBin := buildBin(t, arm64gcc, dir, "belide_arm64_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
