package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWasmRouteProbe covers `wasm_run -decide`, the wasm routing probe.
//
// The wasm drivers exposed only `-ir`, which FORCES the IR path and therefore
// bypasses the gates that decide routing (should_use_ir_core /
// wasm_ir_deferrals_ok / the component pair). So the one question an
// emitter-retirement reroute turns on — "what do the gates DECLINE?" — was
// unanswerable on wasm, while the asm side has had `-ir-probe` and
// asm_pathprobe_run all along.
//
// Enumerating from Go test literals is not a substitute, and the attempt is
// worth recording: a sweep over every backtick literal in the files that
// mention wasm_run.fern produced a confident-looking decline count that was
// almost entirely artifacts — Go comment prose, multi-entry test tables spliced
// into one "program", fmt.Sprintf templates still holding %s/%d, and (the part
// that is not obvious) REAL programs belonging to a DIFFERENT driver, because
// self_host_x86_gas_test.go mentions wasm_run.fern while its programs are x86.
// A file naming a driver does not mean its programs are fed to that driver.
//
// The probe reuses the emitter's own decision rather than restating it
// (wasm_ir.ir_route_precheck / ir_route_final, extracted from
// wasm.emit_module_mode), so it cannot drift from what it describes.
func TestWasmRouteProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// A generic whose body only PASSES the type variable through stays on the IR
	// path (the widened-erased shape); one whose body USES it at the erased width
	// is what module_erased_wide declines. The pair is the point: it pins that the
	// probe reports the gate boundary, not merely "did it parse".
	const passthrough = "function pick2[T](a: T, b: T, useA: boolean): T { if (useA) { return a; } return b; }\n" +
		"function main(): i32 {\n" +
		"    var x: i64 = 5000000000;\n" +
		"    var y: i64 = 7;\n" +
		"    var z: i64 = pick2(x, y, false);\n" +
		"    if (z == 7) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	// A single-typevar fn with a bare-scalar param and a CONCRETE return. Its
	// value never leaves through the type variable, so neither the widening path
	// nor the container promotion covered it, and on wasm it was a SILENT
	// MISCOMPILE: the erased param is i32 there, so an i64 argument crosses the
	// boundary as a box POINTER and `a == b` compared two distinct boxes —
	// returning 1 where the interpreter (and the register backends, whose erased
	// slots are 8 bytes, emitting a `cmpq`) return 42. Parser clause (c'') now
	// promotes the shape so monomorphize_module gives the body a concrete width.
	const usesTypevar = "function eqf[T](a: T, b: T): boolean { return a == b; }\n" +
		"function main(): i32 {\n" +
		"    var x: i64 = 5000000000;\n" +
		"    var y: i64 = 5000000000;\n" +
		"    if (eqf(x, y)) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	// A two-var sibling, also promoted: every var is bound by a bare-scalar param
	// and the return is concrete, so nothing is stranded in the clone.
	const twoTypevars = "function both[T, U](a: T, b: U): boolean { return a == a && b == b; }\n" +
		"function main(): i32 {\n" +
		"    var x: i64 = 5000000000;\n" +
		"    var y: i64 = 5000000000;\n" +
		"    if (both(x, y)) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	// The fold shape: an ARRAY-param type var, so clause (c'') cannot promote it
	// and erased_passthrough_safe excludes it because the body uses the value.
	// Clause (c-arr) promotes on
	// ANY mention of the var in the return, which covers the bare `T` here, so it
	// monomorphises to a concrete `sum_all__i64` and lowers.
	//
	// Worth keeping the history: through the AST emitter this shape returned 1
	// where the interpreter says 42, then became a refusal when that emitter
	// retired, and now it runs and returns 42. The refusal was the right
	// intermediate state — a silent wrong answer is worse than a loud one — but
	// it was never the destination.
	const foldShape = "function sum_all[T](xs: T[], seed: T): T { var acc: T = seed; for x in xs { acc = x; } return acc; }\n" +
		"function main(): i32 {\n" +
		"    var xs: i64[] = [1, 2, 5000000000];\n" +
		"    var s: i64 = sum_all(xs, 0 as i64);\n" +
		"    if (s == 5000000000) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	// STILL declined, and the probe needs a case that is: a TWO-typevar generic
	// over a wide-element array. `U` is bound only by the callback's return, not
	// by any bare-scalar or bare-array param, so promoting `T` alone would leave
	// `U` erased in the clone — the stranded-sibling hazard the `all_tp_count == 1`
	// guard exists for. Left erased, the callee indexes an 8-byte-element array at
	// the i32 stride on wasm32, which is silently wrong rather than a trap, so a
	// refusal is the correct outcome here and not a placeholder for one.
	const twoVarArrayShape = "function map2[T, U](xs: T[], f: (T) => U): U[] {\n" +
		"    var out: U[] = [];\n" +
		"    for x in xs { out = out.append(f(x)); }\n" +
		"    return out;\n" +
		"}\n" +
		"function half(x: i64): i32 { if (x > 1000000000) { return 42; } return 1; }\n" +
		"function main(): i32 {\n" +
		"    var xs: i64[] = [5000000000];\n" +
		"    var ys: i32[] = map2(xs, half);\n" +
		"    return ys[0];\n" +
		"}\n"

	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"plain", "function main(): i32 { return 42; }\n", "ir"},
		{"struct-and-array", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p = P { x: 40, y: 2 }; var a = [p.x, p.y]; return a[0] + a[1]; }\n", "ir"},
		{"erased-wide-passthrough", passthrough, "ir"},
		{"erased-wide-uses-typevar", usesTypevar, "ir"},
		{"erased-wide-two-typevars", twoTypevars, "ir"},
		{"erased-wide-fold-shape", foldShape, "ir"},
		{"erased-wide-two-var-array", twoVarArrayShape, "ast"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(string(runCaptureArgs(t, runner, driverBin, []byte(tc.src), "-decide")))
			if got != tc.want {
				t.Errorf("-decide = %q, want %q", got, tc.want)
			}
		})
	}

	// Both IR-routed programs must RUN to the interpreter's answer. usesTypevar
	// is the regression guard for the box-pointer miscompile described above: it
	// returned 1 before clause (c'').
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"ir-route-runs", passthrough},
		{"erased-wide-uses-typevar-runs", usesTypevar},
		{"erased-wide-two-typevars-runs", twoTypevars},
		// The former declined case. Asserting its VALUE is the point: it returned
		// 1 through the AST emitter, so pinning that emit would have pinned a bug.
		{"erased-wide-fold-shape-runs", foldShape},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Skip("wasmtime not on PATH")
			}
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("wasm exited %d, want 42", code)
			}
		})
	}

	// A declined module is a hard ERROR, which is what retiring wasm.fern bought
	// here: an AST fallback would emit for these and answer wrongly. The
	// contract needs a shape that is genuinely declined to assert against,
	// which is why twoVarArrayShape replaced foldShape when the latter started
	// lowering — a refusal test whose subject no longer refuses proves nothing.
	t.Run("declined-route-refuses", func(t *testing.T) {
		// Not runCapture: that helper fatals on a non-zero exit, and a non-zero
		// exit is exactly the contract here.
		wat, stderr, code := runDeclined(t, runner, driverBin, []byte(twoVarArrayShape))
		if code == 0 || len(wat) != 0 {
			t.Fatalf("driver exited %d with %d bytes, want a refusal — the AST fallback is back, and on this shape it miscompiles", code, len(wat))
		}
		if !strings.Contains(stderr, "not IR-eligible") {
			t.Errorf("refusal did not say the module is ineligible:\n%s", stderr)
		}
	})
}

// runDeclined runs the driver expecting it to REFUSE, returning stdout, stderr
// and the exit code rather than fataling on a non-zero exit.
func runDeclined(t *testing.T, runner []string, bin string, stdin []byte) ([]byte, string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.Bytes(), stderr.String(), cmd.ProcessState.ExitCode()
}

// runCaptureArgs is runCapture with extra argv for the driver — the probe needs
// `-decide`, and every other wasm driver call in the suite passes none.
func runCaptureArgs(t *testing.T, runner []string, bin string, stdin []byte, args ...string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("driver: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.Bytes()
}
