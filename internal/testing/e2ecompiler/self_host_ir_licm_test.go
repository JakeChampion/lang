package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostIRLICM(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("ir_licm_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ir_licm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/ir_licm_run.fern", "ir_licm_run")

	const want = "" +
		"licm_while_cond: block ; load_local 0 ; str_len ; store_local 3 ; loop ; load_local 1 ; load_local 3 ; lt_s ; not ; brif 1 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end ; load_local 1 ; return | n_locals=4\n" +
		"licm_mutated_refused: block ; loop ; load_local 1 ; load_local 0 ; str_len ; lt_s ; not ; brif 1 ; load_local 2 ; store_local 0 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=3\n" +
		"licm_body_refused: block ; loop ; load_local 1 ; load_local 2 ; lt_s ; not ; brif 1 ; load_local 3 ; load_local 0 ; str_len ; add ; store_local 3 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=4\n" +
		"licm_idempotent=1\n" +
		"licm_two_slots: block ; load_local 0 ; str_len ; store_local 3 ; load_local 1 ; str_len ; store_local 4 ; loop ; load_local 2 ; load_local 3 ; load_local 4 ; add ; lt_s ; not ; brif 1 ; load_local 2 ; const_i32 1 ; add ; store_local 2 ; br 0 ; end ; end | n_locals=5\n" +
		"licm_two_slots_stack_neutral=1\n" +
		"licm_same_operand_once: block ; load_local 0 ; str_len ; store_local 3 ; loop ; load_local 1 ; load_local 3 ; load_local 3 ; add ; lt_s ; not ; brif 1 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=4\n" +
		"licm_short_circuit: block ; load_local 0 ; str_len ; store_local 4 ; loop ; load_local 2 ; load_local 4 ; lt_s ; store_local 3 ; block ; load_local 3 ; not ; brif 0 ; load_local 2 ; load_local 1 ; str_len ; lt_s ; store_local 3 ; end ; load_local 3 ; not ; brif 1 ; load_local 2 ; const_i32 1 ; add ; store_local 2 ; br 0 ; end ; end | n_locals=5\n" +
		"licm_short_circuit_stack_neutral=1\n" +
		"licm_typed_block_header: block ; load_local 0 ; str_len ; store_local 4 ; loop ; block ; load_local 4 ; store_local 3 ; load_local 1 ; load_local 3 ; lt_s ; not ; brif 2 ; end ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=5\n" +
		"licm_typed_block_header_stack_neutral=1\n" +
		"licm_exit_refused: block ; loop ; block ; const_i32 134 ; exit ; load_local 1 ; load_local 0 ; str_len ; lt_s ; not ; brif 2 ; end ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=3\n" +
		"licm_nested: block ; load_local 0 ; str_len ; store_local 5 ; loop ; load_local 2 ; load_local 5 ; lt_s ; not ; brif 1 ; block ; load_local 1 ; str_len ; store_local 4 ; loop ; load_local 3 ; load_local 4 ; lt_s ; not ; brif 1 ; load_local 3 ; const_i32 1 ; add ; store_local 3 ; br 0 ; end ; end ; load_local 2 ; const_i32 1 ; add ; store_local 2 ; br 0 ; end ; end | n_locals=6\n" +
		"licm_data_address: block ; load_local 0 ; str_len ; store_local 3 ; load_local 0 ; str_data ; store_local 4 ; loop ; load_local 1 ; load_local 3 ; lt_s ; not ; brif 1 ; load_local 2 ; load_local 4 ; load_local 1 ; raw_load8 ; add ; store_local 2 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=5\n" +
		"licm_data_address_stack_neutral=1\n" +
		"licm_data_unread_refused: block ; loop ; load_local 1 ; load_local 3 ; lt_s ; not ; brif 1 ; load_local 2 ; load_local 0 ; str_data ; load_local 1 ; raw_load8 ; add ; store_local 2 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=4\n" +
		"licm_data_mutated_refused: block ; loop ; load_local 1 ; load_local 0 ; str_len ; lt_s ; not ; brif 1 ; load_local 2 ; store_local 0 ; load_local 2 ; load_local 0 ; str_data ; load_local 1 ; raw_load8 ; add ; store_local 2 ; load_local 1 ; const_i32 1 ; add ; store_local 1 ; br 0 ; end ; end | n_locals=3\n" +
		"licm_unclosed_refused: block ; loop ; load_local 1 ; load_local 0 ; str_len ; lt_s ; not ; brif 1 | n_locals=3\n" +
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

var licmPrograms = []struct {
	name, src       string
	want            int
	outside, inside int
}{
	{"while-cond",
		`function scan(s: string): i32 { let i: i32 = 0; let n: i32 = 0; while (i < s.len()) { if (s[i] == b'#') { n = n + 1; } i = i + 1; } return n; } function main(): i32 { return scan("a#b##c"); }`,
		3, 1, 0},
	{"mutated-operand",
		`function grow(a: string): i32 { let s: string = a; let i: i32 = 0; while (i < s.len()) { if (i == 0) { s = a + "x"; } i = i + 1; } return i; } function main(): i32 { return grow("ab"); }`,
		3, 0, 1},
	{"body-only",
		`function total(s: string, k: i32): i32 { let i: i32 = 0; let n: i32 = 0; while (i < k) { n = n + s.len(); i = i + 1; } return n; } function main(): i32 { return total("abc", 4); }`,
		12, 0, 1},
	{"two-slots",
		`function f(s: string, t: string): i32 { let i: i32 = 0; while (i < s.len() + t.len()) { i = i + 1; } return i; } function main(): i32 { return f("abc", "de"); }`,
		5, 2, 0},
	{"short-circuit",
		`function f(a: string, b: string): i32 { let i: i32 = 0; while (i < a.len() && i < b.len()) { i = i + 1; } return i; } function main(): i32 { return f("abcd", "de"); }`,
		2, 1, 1},
}

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

var selfHostLenRead = regexp.MustCompile(`^movq 8\(%[a-z0-9]+\), %[a-z0-9]+$`)

var selfHostJump = regexp.MustCompile(`^j[a-z]+ (\.Lssa_\S+)$`)

func selfHostLenShape(asm string) (outside, inside int) {
	backEdge := map[string]bool{}
	seen := map[string]bool{}
	for _, raw := range strings.Split(asm, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, ".Lssa_") && strings.HasSuffix(line, ":") {
			seen[strings.TrimSuffix(line, ":")] = true
		}
		if m := selfHostJump.FindStringSubmatch(line); m != nil && seen[m[1]] {
			backEdge[m[1]] = true
		}
	}
	return asmLenShape(asm,
		func(l string) bool { return selfHostLenRead.MatchString(l) && !strings.Contains(l, "(%rsp)") },
		func(l string) (string, bool) {
			label := strings.TrimSuffix(l, ":")
			return label, strings.HasPrefix(l, ".Lssa_") && strings.HasSuffix(l, ":") && backEdge[label]
		},
		func(l, label string) bool {
			m := selfHostJump.FindStringSubmatch(l)
			return m != nil && m[1] == label
		})
}

func TestSelfHostLICMX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	asmBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "asm_ir_run")

	for _, tc := range licmPrograms {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, asmBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if out, in := selfHostLenShape(string(asm)); out != tc.outside || in != tc.inside {
				t.Errorf("self-host asm: %d length reads outside loops and %d inside, want %d / %d:\n%s", out, in, tc.outside, tc.inside, asm)
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

func TestSelfHostLICMArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostLICMWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host LICM wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range licmPrograms {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(runner, driverBin)
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
