package e2ecompiler

import (
	"strings"
	"testing"
)

// hotLoopMid is the middle-snake search of diffutils/diff.fern in small: a
// forward and a backward diagonal loop, each with a snake loop inside, and a
// return from each loop that reads the snake's x and y. EXIT is the value the
// forward return stores.
const hotLoopMid = `@noinline function mid(own v: i32[], xv: i32[], yv: i32[], fo: i32, bo: i32, xoff: i32, xlim: i32, yoff: i32, ylim: i32, k: i32): i32[] {
    let dmin = xoff - ylim;
    let dmax = xlim - yoff;
    let fmin = xoff - yoff;
    let fmax = fmin;
    let bmin = xlim - ylim;
    let bmax = bmin;
    v = v.with(fo + fmin, xoff);
    v = v.with(bo + bmin, xlim);
    let c = 0;
    let big = 0;
    let odd = (fmin - bmin & 1) != 0;
    while (c < 40) {
        if (fmin > dmin) { fmin -= 1; v = v.with(fo + fmin - 1, -1); } else { fmin += 1; }
        if (fmax < dmax) { fmax += 1; v = v.with(fo + fmax + 1, -1); } else { fmax -= 1; }
        let d = fmax;
        while (d >= fmin) {
            let tlo = v[fo + d - 1];
            let thi = v[fo + d + 1];
            let x0 = if (tlo < thi) { thi } else { tlo + 1 };
            let x = x0;
            let y = x0 - d;
            while (x < xlim && y < ylim && xv[x] == yv[y]) {
                x += 1;
                y += 1;
            }
            if (x - x0 > k) { big += 1; }
            v = v.with(fo + d, x);
            if (odd && bmin <= d && d <= bmax && v[bo + d] <= x) {
                return v.with(0, EXIT).with(1, y);
            }
            d -= 2;
        }
        if (bmin > dmin) { bmin -= 1; v = v.with(bo + bmin - 1, 2147483647); } else { bmin += 1; }
        if (bmax < dmax) { bmax += 1; v = v.with(bo + bmax + 1, 2147483647); } else { bmax -= 1; }
        d = bmax;
        while (d >= bmin) {
            let tlo = v[bo + d - 1];
            let thi = v[bo + d + 1];
            let x0 = if (tlo < thi) { tlo } else { thi - 1 };
            let x = x0;
            let y = x0 - d;
            while (xoff < x && yoff < y && xv[x - 1] == yv[y - 1]) {
                x -= 1;
                y -= 1;
            }
            if (x0 - x > k) { big += 1; }
            v = v.with(bo + d, x);
            if (!odd && fmin <= d && d <= fmax && x <= v[fo + d]) {
                return v.with(0, x).with(1, y);
            }
            d -= 2;
        }
        if (big > 30 + c) { return v.with(2, big); }
        c += 1;
    }
    return v;
}
`

func hotLoopProgram(exit, xv, yv, lims string) string {
	return strings.Replace(hotLoopMid, "EXIT", exit, 1) + `
function main(): i32 {
    let xv: i32[] = ` + xv + `;
    let yv: i32[] = ` + yv + `;
    let n = xv.len();
    let m = yv.len();
    let diags = n + m + 3;
    let v = __alloc_i32(2 * diags + 8);
    v = mid(v, xv, yv, m + 1, diags + m + 1, ` + lims + `, 3);
    return (v[0] * 10 + v[1] + v[2]) % 120;
}
`
}

var hotLoopCases = []struct {
	name string
	src  string
}{
	{"forward_exit_reads_x", hotLoopProgram("x", "[1, 2, 3, 4, 5, 6, 7, 8]", "[9, 2, 3, 9, 5, 9, 7, 9, 4]", "0, n, 0, m")},
	{"forward_exit_reads_d", hotLoopProgram("d", "[1, 2, 3, 4, 5, 6, 7, 8]", "[9, 2, 3, 9, 5, 9, 7, 9, 4]", "0, n, 0, m")},
	{"odd_box", hotLoopProgram("x", "[3, 1, 4, 1, 5, 9, 2]", "[2, 7, 1, 8, 2, 8, 1, 8, 2, 8]", "0, n, 0, m")},
	{"long_snakes", hotLoopProgram("x", "[1, 2, 3, 4, 5, 6, 7, 8, 9, 10]", "[0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10]", "0, n, 0, m")},
}

// The forward snake of this box runs past the end of xv: the bound it tests
// is xlim, which the caller sets above the length.
var hotLoopOutOfRange = hotLoopProgram("x", "[1, 2, 3, 4, 5, 6, 7, 8]", "[1, 2, 3, 4, 5, 6, 7, 8, 9, 9, 9, 9]", "0, n + 3, 0, m")

func TestSelfHostSSAHotLoopX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	build := func(t *testing.T, name, src string) string {
		asm := string(runCapture(t, gcc, runner, driverBin, []byte(src)))
		if !strings.Contains(asm, ".Lssa_") {
			t.Fatalf("did not lower through the IR (no .Lssa_ labels)")
		}
		return buildBin(t, gcc, dir, name, asm)
	}
	for _, tc := range hotLoopCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if _, code := runCaptureStderrExit(t, runner, build(t, "hotloop_"+tc.name, tc.src)); code != want {
				t.Errorf("exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
	t.Run("snake_out_of_range_aborts", func(t *testing.T) {
		stderr, code := runCaptureStderrExit(t, runner, build(t, "hotloop_oob", hotLoopOutOfRange))
		if code != 134 || !strings.Contains(stderr, "array index out of range") {
			t.Errorf("exited %d with %q, want 134 and the bounds cause line", code, stderr)
		}
	})
}

func TestSelfHostSSAHotLoopArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	run := func(t *testing.T, name, src string) (string, int) {
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("the self-host arm64 compiler emitted nothing")
		}
		cmd := runArm64Bin(qemu, buildBin(t, arm64gcc, dir, name, string(asm)))
		var errBuf strings.Builder
		cmd.Stderr = &errBuf
		_ = cmd.Run()
		return errBuf.String(), cmd.ProcessState.ExitCode()
	}
	for _, tc := range hotLoopCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if _, code := run(t, "hotloop_arm64_"+tc.name, tc.src); code != want {
				t.Errorf("arm64 exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
	t.Run("snake_out_of_range_aborts", func(t *testing.T) {
		stderr, code := run(t, "hotloop_arm64_oob", hotLoopOutOfRange)
		if code != 134 || !strings.Contains(stderr, "array index out of range") {
			t.Errorf("arm64 exited %d with %q, want 134 and the bounds cause line", code, stderr)
		}
	})
}
