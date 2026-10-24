package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var localLabelRe = regexp.MustCompile(`\.Lssa_[A-Za-z0-9_.$]+`)

func assertNoDanglingLocalLabels(t *testing.T, ctx string, asm []byte) {
	t.Helper()
	if dangling := danglingLocalLabels(asm); len(dangling) > 0 {
		t.Fatalf("%s: dangling local label(s) referenced but never defined: %v", ctx, dangling)
	}
}

func danglingLocalLabels(asm []byte) []string {
	defined := map[string]bool{}
	referenced := map[string]bool{}
	for _, line := range strings.Split(string(asm), "\n") {
		trimmed := strings.TrimSpace(line)
		if lbl, ok := strings.CutSuffix(trimmed, ":"); ok && localLabelRe.MatchString(lbl) && !strings.ContainsAny(lbl, " \t") {
			defined[lbl] = true
			continue
		}
		for _, m := range localLabelRe.FindAllString(line, -1) {
			referenced[m] = true
		}
	}
	var dangling []string
	for r := range referenced {
		if !defined[r] {
			dangling = append(dangling, r)
		}
	}
	sort.Strings(dangling)
	return dangling
}

var blockLabelRe = regexp.MustCompile(`\.Lssa_[A-Za-z0-9_.$]+`)

func assertNoDuplicateLocalLabels(t *testing.T, ctx string, asm []byte) {
	t.Helper()
	if dup := duplicateLocalLabels(asm); len(dup) > 0 {
		t.Fatalf("%s: local label(s) defined more than once: %v", ctx, dup)
	}
}

func duplicateLocalLabels(asm []byte) []string {
	seen := map[string]int{}
	var dup []string
	for _, line := range strings.Split(string(asm), "\n") {
		lbl, ok := strings.CutSuffix(strings.TrimSpace(line), ":")
		if !ok || strings.ContainsAny(lbl, " \t") || !blockLabelRe.MatchString(lbl) {
			continue
		}
		seen[lbl]++
		if seen[lbl] == 2 {
			dup = append(dup, lbl)
		}
	}
	sort.Strings(dup)
	return dup
}

var readFileIRCases = []struct {
	name, src, wantOut string
	wantExit           int
}{
	{"len", `function main(): i32 { match (read_file("rf_data.txt")) { Ok(s) => { return s.len(); }, Err(e) => { return 99; } } return 0; }`, "", 5},
	{"echo", `function main(): i32 { match (read_file("rf_data.txt")) { Ok(s) => { write(s); return 0; }, Err(e) => { return 1; } } return 0; }`, "hello", 0},
	{"missing", `function main(): i32 { match (read_file("rf_nope.txt")) { Ok(s) => { return 0; }, Err(e) => { return 42; } } return 0; }`, "", 42},
	{"bind", `function main(): i32 { let r = read_file("rf_data.txt"); match (r) { Ok(s) => { return s.len(); }, Err(e) => { return 7; } } return 0; }`, "", 5},
}

func writeRFData(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "rf_data.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write rf_data.txt: %v", err)
	}
}

func TestSelfHostReadFileIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	for _, tc := range readFileIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fn___fern_read_file")) {
				t.Fatalf("%s: no call to __fn___fern_read_file — did not lower through the IR path", tc.name)
			}
			assertNoDanglingLocalLabels(t, tc.name, asm)
			assertNoDuplicateLocalLabels(t, tc.name, asm)
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Dir = dir
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			_ = cmd.Run()
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("%s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("%s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

func TestSelfHostReadFileIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host read_file wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")
	for _, tc := range readFileIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(wat, []byte("call $__fern_read_file")) {
				t.Fatalf("%s: no `call $__fern_read_file` — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "rf_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
			run.Dir = dir
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: wasmtime did not exit normally:\n%s", tc.name, wat)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_file wasm IR %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_file wasm IR %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}

func TestSelfHostReadFileIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	writeRFData(t, dir)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	for _, tc := range readFileIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64-linux")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fn___fern_read_file")) {
				t.Fatalf("%s: no bl __fn___fern_read_file — did not lower through the arm64 IR path", tc.name)
			}
			assertNoDanglingLocalLabels(t, "arm64 "+tc.name, asm)
			assertNoDuplicateLocalLabels(t, "arm64 "+tc.name, asm)
			bin := buildBinArm64(t, arm64gcc, dir, "rf_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			run.Dir = dir
			var stdout bytes.Buffer
			run.Stdout = &stdout
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if tc.wantOut != "" || tc.wantExit == 0 {
				if stdout.String() != tc.wantOut {
					t.Errorf("read_file arm64 %s: stdout %q, want %q", tc.name, stdout.String(), tc.wantOut)
				}
			}
			if tc.wantOut == "" && tc.wantExit != 0 {
				if code := run.ProcessState.ExitCode(); code != tc.wantExit {
					t.Errorf("read_file arm64 %s: exit %d, want %d", tc.name, code, tc.wantExit)
				}
			}
		})
	}
}
