package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIRLICM pins the self-hosted stack IR's loop-invariant code motion
// (examples/self_host/ir.fern's hoist_loop_invariants — the op-list twin of
// native's internal/ir/licm.go, #8245) and the slot growth it rides on (#8247).
//
// The ir_licm_run driver builds the op list irlower emits for each `while`
// shape, runs the pass, and prints the ops AND the frame count. Every line
// below is the mirror of a case in internal/ir/licm_test.go; the refusals are
// the load-bearing half, since each one is a read the original program would
// not have made.
func TestSelfHostIRLICM(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ir_licm_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ir_licm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "ir_licm_run.fern", "ir_licm_run")

	const want = "" +
		// The length is read once, before the loop, into the fresh slot 3, and
		// the header reads the cache. The frame grows 3 -> 4.
		"licm_while_cond: block ; load_local 0 ; str_len ; store_local 3 ; loop ; load_local 1 ; load_local 3 ; lt_s ; not ; brif 1 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end ; load_local 1 ; return | n_locals=4\n" +
		// The body stores to the operand, so its length is not invariant.
		"licm_mutated_refused: block ; loop ; load_local 1 ; load_local 0 ; str_len ; lt_s ; not ; brif 1 ; load_local 2 ; store_local 0 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=3\n" +
		// A read from the body rather than the header stays where it is.
		"licm_body_refused: block ; loop ; load_local 1 ; load_local 2 ; lt_s ; not ; brif 1 ; load_local 3 ; load_local 0 ; str_len ; add ; store_local 3 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=4\n" +
		"licm_idempotent=1\n" +
		// Two operands take two slots, in read order, and the prologue is
		// stack-net-zero.
		"licm_two_slots: block ; load_local 0 ; str_len ; store_local 3 ; load_local 1 ; str_len ; store_local 4 ; loop ; load_local 2 ; load_local 3 ; load_local 4 ; add ; lt_s ; not ; brif 1 ; load_local 2 ; const_i32 1 ; add ; store_local 2 ; br 0 ; end ; end | n_locals=5\n" +
		"licm_two_slots_stack_neutral=1\n" +
		"licm_same_operand_once: block ; load_local 0 ; str_len ; store_local 3 ; loop ; load_local 1 ; load_local 3 ; load_local 3 ; add ; lt_s ; not ; brif 1 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=4\n" +
		// `&&` opens a block that ends the header: the guarded length stays.
		"licm_short_circuit: block ; load_local 0 ; str_len ; store_local 4 ; loop ; load_local 2 ; load_local 4 ; lt_s ; store_local 3 ; block ; load_local 3 ; not ; brif 0 ; load_local 2 ; load_local 1 ; str_len ; lt_s ; store_local 3 ; end ; load_local 3 ; not ; brif 1 ; load_local 2 ; const_i32 1 ; add ; store_local 2 ; br 0 ; end ; end | n_locals=5\n" +
		"licm_short_circuit_stack_neutral=1\n" +
		// Later loops first: the inner loop takes slot 4, the outer slot 5, and
		// each prologue sits directly before its own `loop`.
		"licm_nested: block ; load_local 0 ; str_len ; store_local 5 ; loop ; load_local 2 ; load_local 5 ; lt_s ; not ; brif 1 ; block ; load_local 1 ; str_len ; store_local 4 ; loop ; load_local 3 ; load_local 4 ; lt_s ; not ; brif 1 ; load_local 3 ; const_i32 1 ; add ; store_local 3 ; br 0 ; end ; end ; load_local 2 ; const_i32 1 ; add ; store_local 2 ; br 0 ; end ; end | n_locals=6\n" +
		"licm_unclosed_refused: block ; loop ; load_local 1 ; load_local 0 ; str_len ; lt_s ; not ; brif 1 | n_locals=3\n" +
		// #8247: against the count the pass returned the ops verify clean;
		// against the lowering's own count both ops naming the cache slot are
		// out of range — the refusal a frame one slot short earns.
		"licm_frame_grown: new=0 old=2\n" +
		"licm_in_optimize: block ; load_local 0 ; str_len ; store_local 3 ; loop ; load_local 1 ; load_local 3 ; lt_s ; not ; brif 1 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end ; load_local 1 ; return | n_locals=4\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("ir_licm_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("LICM report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("ir_licm_run exit code = %d, want 0 (a second optimize_ops round changed the hoisted result)", code)
	}
}

// licmPrograms are the fixtures of internal/ir/licm_test.go with a main, so
// the same source is lowered, emitted and run. `outside` / `inside` count the
// string-length reads the pass leaves outside any loop and inside one; the
// runtime value is what every backend must still compute.
var licmPrograms = []struct {
	name, fn, src   string
	want            int
	outside, inside int
}{
	{"while-cond", "scan",
		`function scan(s: string): i32 { var i: i32 = 0; var n: i32 = 0; while (i < s.len()) { if (s[i] == b'#') { n = n + 1; } i = i + 1; } return n; } function main(): i32 { return scan("a#b##c"); }`,
		3, 1, 0},
	// The operand is reassigned in the body, so the length is re-read each
	// iteration: the loop runs to the NEW string's length (3), not the old (2).
	{"mutated-operand", "grow",
		`function grow(a: string): i32 { var s: string = a; var i: i32 = 0; while (i < s.len()) { if (i == 0) { s = a + "x"; } i = i + 1; } return i; } function main(): i32 { return grow("ab"); }`,
		3, 0, 1},
	{"body-only", "total",
		`function total(s: string, k: i32): i32 { var i: i32 = 0; var n: i32 = 0; while (i < k) { n = n + s.len(); i = i + 1; } return n; } function main(): i32 { return total("abc", 4); }`,
		12, 0, 1},
	{"two-slots", "f",
		`function f(s: string, t: string): i32 { var i: i32 = 0; while (i < s.len() + t.len()) { i = i + 1; } return i; } function main(): i32 { return f("abc", "de"); }`,
		5, 2, 0},
	{"short-circuit", "f",
		`function f(a: string, b: string): i32 { var i: i32 = 0; while (i < a.len() && i < b.len()) { i = i + 1; } return i; } function main(): i32 { return f("abcd", "de"); }`,
		2, 1, 1},
}

// irLenShape counts the `str_len` ops of an irlower_run `-dump-fn` op stream
// outside and inside its first loop.
func irLenShape(t *testing.T, dump string) (outside, inside int) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(dump), "\n")
	loopAt, loopEnd := -1, -1
	depth := 0
	for i, l := range lines {
		switch {
		case l == "loop" && loopAt < 0:
			loopAt = i
			depth = 1
		case (l == "block" || l == "loop" || l == "if") && loopAt >= 0 && loopEnd < 0:
			depth++
		case l == "end" && loopAt >= 0 && loopEnd < 0:
			depth--
			if depth == 0 {
				loopEnd = i
			}
		}
	}
	if loopAt < 0 || loopEnd < 0 {
		t.Fatalf("no closed loop in the op stream:\n%s", dump)
	}
	for i, l := range lines {
		if l != "str_len" {
			continue
		}
		if i > loopAt && i < loopEnd {
			inside++
		} else {
			outside++
		}
	}
	return outside, inside
}

// asmLenShape counts string-length reads in the user functions of an x86-64
// listing, outside and inside loops. Each compiler spells the read and the
// loop differently, so the caller names both: `marker` is a line that is one
// read, `loopTop` the label opening a loop, and `loopEnd` whether a line
// closes the loop that label opened. User functions are the `__fn_*` labels;
// the self-host also emits its runtime helpers under `__fn___fern_*`, and
// those read lengths of their own.
func asmLenShape(asm string, marker func(string) bool, loopTop func(string) (string, bool), loopEnd func(line, label string) bool) (outside, inside int) {
	inFn := false
	var open []string
	for _, raw := range strings.Split(asm, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "__fn_") && strings.HasSuffix(line, ":"):
			inFn = !strings.HasPrefix(line, "__fn___fern_")
			open = nil
			continue
		case line == ".cfi_endproc":
			inFn = false
			continue
		}
		if !inFn {
			continue
		}
		if label, ok := loopTop(line); ok {
			open = append(open, label)
		}
		if marker(line) {
			if len(open) > 0 {
				inside++
			} else {
				outside++
			}
		}
		if len(open) > 0 && loopEnd(line, open[len(open)-1]) {
			open = open[:len(open)-1]
		}
	}
	return outside, inside
}

// selfHostLenShape reads the self-host x86-64 backend's listing: a length is
// the `movq 8(%rax), %rax` that follows the box pop, and a loop runs from a
// `.Lir_` label to the `jmp` back to it. Labels are only loop tops when a jump
// later in the function targets them, so every label is opened on sight and
// closed by its back edge; a forward-only label simply never closes, which is
// why the set is cleared at each function.
func selfHostLenShape(asm string) (outside, inside int) {
	backEdge := map[string]bool{}
	seen := map[string]bool{}
	for _, raw := range strings.Split(asm, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, ".Lir_") && strings.HasSuffix(line, ":") {
			seen[strings.TrimSuffix(line, ":")] = true
		}
		if strings.HasPrefix(line, "jmp .Lir_") && seen[strings.TrimPrefix(line, "jmp ")] {
			backEdge[strings.TrimPrefix(line, "jmp ")] = true
		}
	}
	return asmLenShape(asm,
		func(l string) bool { return l == "movq 8(%rax), %rax" },
		func(l string) (string, bool) {
			label := strings.TrimSuffix(l, ":")
			return label, strings.HasPrefix(l, ".Lir_") && strings.HasSuffix(l, ":") && backEdge[label]
		},
		func(l, label string) bool { return l == "jmp "+label })
}

// nativeLenShape reads the native x86-64 backend's listing: a length is one
// `.Lstrlen_inline_N` label (the small-string arm of the tag test), and a loop
// runs from `.LloopTop_N` to `.LloopEnd_N`.
func nativeLenShape(asm string) (outside, inside int) {
	return asmLenShape(asm,
		func(l string) bool { return strings.HasPrefix(l, ".Lstrlen_inline_") && strings.HasSuffix(l, ":") },
		func(l string) (string, bool) {
			if strings.HasPrefix(l, ".LloopTop_") && strings.HasSuffix(l, ":") {
				return strings.TrimSuffix(strings.TrimPrefix(l, ".LloopTop_"), ":"), true
			}
			return "", false
		},
		func(l, n string) bool { return l == ".LloopEnd_"+n+":" })
}

// TestSelfHostLICMX86_64 is the source-level half: each fixture is lowered by
// the self-host and the hoist read off the op stream, the emitted x86-64 is
// checked for the same shape native emits for the same program, and the
// binary runs to the value the unhoisted program computed.
func TestSelfHostLICMX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "irlower_run.fern", "asm_ir_run.fern")
	lowerBin := buildSelfHostBin(t, gcc, dir, "irlower_run.fern", "irlower_run")
	asmBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "asm_ir_run")
	nativeBin := buildFernCLIBin(t)

	for _, tc := range licmPrograms {
		t.Run(tc.name, func(t *testing.T) {
			dump := runIRLower(t, lowerBin, "-dump-fn", tc.src, tc.fn)
			if out, in := irLenShape(t, dump); out != tc.outside || in != tc.inside {
				t.Errorf("IR: %d str_len outside the loop and %d inside, want %d / %d:\n%s", out, in, tc.outside, tc.inside, dump)
			}

			asm := runCapture(t, gcc, runner, asmBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if out, in := selfHostLenShape(string(asm)); out != tc.outside || in != tc.inside {
				t.Errorf("self-host asm: %d length reads outside loops and %d inside, want %d / %d:\n%s", out, in, tc.outside, tc.inside, asm)
			}

			srcPath := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write source: %v", err)
			}
			nativeAsm, err := exec.Command(nativeBin, "-target", "x86-64-linux", srcPath).Output()
			if err != nil {
				t.Fatalf("native emit: %v", err)
			}
			if out, in := nativeLenShape(string(nativeAsm)); out != tc.outside || in != tc.inside {
				t.Errorf("native asm: %d length reads outside loops and %d inside, want %d / %d — the two compilers no longer hoist the same shape:\n%s", out, in, tc.outside, tc.inside, nativeAsm)
			}

			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d", code, tc.want)
			}
		})
	}
}

// TestSelfHostLICMArm64 runs the same fixtures through the self-host arm64
// backend under qemu: the hoisted slot is one past the lowering's count, so
// this is the frame-size half of #8247 on the second register backend.
func TestSelfHostLICMArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range licmPrograms {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "licm_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exited %d, want %d", code, tc.want)
			}
		})
	}
}

// TestSelfHostLICMWasmIR runs the fixtures through the self-host wasm backend
// under wasmtime, where the cache slot is a declared `(local i32)` past the
// body locals and wasm validation would reject an index the frame did not
// declare.
func TestSelfHostLICMWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host LICM wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range licmPrograms {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(runner, driverBin, "-ir")
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, "licm_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("exited %d, want %d", got, tc.want)
			}
		})
	}
}
