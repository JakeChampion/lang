package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func wasmLcCompile(t *testing.T, runner []string, driverBin, src string, env []string) string {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm driver failed: %v", err)
	}
	return string(wat)
}

func wasmLcRun(t *testing.T, dir, name, wat string) (stderr string, exit int) {
	t.Helper()
	watFile := filepath.Join(dir, name+".wat")
	if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watFile)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally for %q", name)
	}
	return errBuf.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostWasmLeakcheck(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm leakcheck e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name    string
		src     string
		want    int
		verdict string
	}{
		{
			name: "clean_string_churn",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); return t.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "balanced",
		},
		{
			name: "alias_reassign_balanced",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); let v: string = t; v = w("cd"); return v.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 21, verdict: "balanced",
		},
		{
			name: "leak_raw_alloc",
			src: `function round(i: i32): i32 { let p: usize = __alloc(24); return i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`,
			want: 53, verdict: "leaky",
		},
		{
			name: "heapfree_return",
			src:  `function main(): i32 { return 7; }`,
			want: 7, verdict: "zero",
		},
		{
			name: "heapfree_exit_builtin",
			src:  `function main(): i32 { exit(9); return 0; }`,
			want: 9, verdict: "zero",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stderr, exit := wasmLcRun(t, dir, "lc_"+tc.name, wat)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d — the census must not disturb the program", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary on stderr (%q)", tc.name, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if got, want := strings.HasSuffix(summary, " from=exit"), tc.name == "heapfree_exit_builtin"; got != want {
				t.Errorf("%s: census %q says from=exit %v, want %v", tc.name, summary, got, want)
			}
			switch tc.verdict {
			case "balanced":
				if allocs == 0 {
					t.Errorf("%s: allocs=0 — the probe exercised no allocation", tc.name)
				}
				if allocs != frees || live != 0 {
					t.Errorf("%s: %s — want a balanced census", tc.name, summary)
				}
			case "leaky":
				if allocs <= frees || live <= 0 {
					t.Errorf("%s: %s — this shape leaks soundly per round; a balanced census "+
						"means a bump site is miscounting", tc.name, summary)
				}
			case "zero":
				if allocs != 0 || frees != 0 || live != 0 {
					t.Errorf("%s: %s — a heap-free program must report zeros", tc.name, summary)
				}
			}
		})
	}
}

func wasmLcInvoke(t *testing.T, dir, name, wat string) (stdout, stderr string, exit int) {
	t.Helper()
	watFile := filepath.Join(dir, name+".wat")
	if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", watFile)
	var outBuf, errBuf strings.Builder
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally for %q", name)
	}
	return outBuf.String(), errBuf.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostWasmSanitizeLeakVerdict(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm sanitize e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")
	verdict := regexp.MustCompile(`fern-sanitizer: leak (\d+) bytes in (\d+) blocks\n`)

	leaky := `function round(i: i32): i32 { let p: usize = __alloc(24); return i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`
	wat := wasmLcCompile(t, runner, driverBin, leaky, []string{"FERN_SANITIZE=1"})
	stderr, exit := wasmLcRun(t, dir, "san_leaky", wat)
	if exit != 53 {
		t.Fatalf("leaky exited %d, want 53 — a leak verdict must not move the exit status", exit)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != 100 || frees != 0 || live != 2400 {
		t.Fatalf("leaky census %q, want allocs=100 frees=0 live_bytes=2400 (one 24-byte block per round, never freed)", leakSummaryLine(stderr))
	}
	m := verdict.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no leak verdict in stderr: %q", stderr)
	}
	if m[1] != strconv.FormatInt(live, 10) || m[2] != strconv.FormatInt(allocs-frees, 10) {
		t.Errorf("verdict %q disagrees with the summary %q", m[0], leakSummaryLine(stderr))
	}

	clean := `function main(): i32 { let i: i32 = 0; while (i < 100) { let a: usize = __alloc(64); __free(a, 64); i = i + 1; } return 0; }`
	wat = wasmLcCompile(t, runner, driverBin, clean, []string{"FERN_SANITIZE=1"})
	stderr, exit = wasmLcRun(t, dir, "san_clean", wat)
	if exit != 0 {
		t.Fatalf("clean exited %d, want 0", exit)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("clean program drew a verdict: %q", stderr)
	}
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != 100 || frees != 100 || live != 0 {
		t.Errorf("clean census %q, want allocs=100 frees=100 live_bytes=0", leakSummaryLine(stderr))
	}
}

func TestSelfHostWasmLeakcheckReportsOnInvoke(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm leakcheck invoke e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name, src, want string
	}{
		{"returning", `function main(): i32 { let p: usize = __alloc(24); return 7; }`, "7"},
		{"exiting", `function main(): i32 { let p: usize = __alloc(24); exit(9); return 1; }`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stdout, stderr, exit := wasmLcInvoke(t, dir, "invoke_"+tc.name, wat)
			if tc.want != "" && (exit != 0 || strings.TrimSpace(stdout) != tc.want) {
				t.Fatalf("exit %d stdout %q, want 0 and %q; stderr: %s", exit, stdout, tc.want, stderr)
			}
			if tc.want == "" && exit != 9 {
				t.Fatalf("exit %d, want 9 (exit() carries its status); stderr: %s", exit, stderr)
			}
			if n := strings.Count(stderr, "leakcheck: allocs="); n != 1 {
				t.Fatalf("%d census lines, want exactly 1: %q", n, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != 1 || frees != 0 || live != 24 {
				t.Errorf("census %q, want allocs=1 frees=0 live_bytes=24", leakSummaryLine(stderr))
			}
		})
	}
}

func TestSelfHostWasmLeakcheckOffEmitsNothing(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm leakcheck off e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let t: string = w("ab"); return t.len() + i; }
function main(): i32 { let x: i32 = 0; let r: i32 = 0; while (r < 100) { x = x + round(r); r = r + 1; } return x % 83; }`

	off := wasmLcCompile(t, runner, driverBin, src, nil)
	for _, marker := range []string{"__fern_lc_", "__fern_proc_exit_raw", "leakcheck"} {
		if strings.Contains(off, marker) {
			t.Errorf("flag-off wat contains %q — the census is not fully gated", marker)
		}
	}
	stderr, exit := wasmLcRun(t, dir, "lc_off", off)
	if exit != 21 {
		t.Fatalf("flag-off run exited %d, want 21", exit)
	}
	if stderr != "" {
		t.Errorf("flag-off run wrote stderr: %q", stderr)
	}

	on := wasmLcCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	for _, want := range []string{"$__fern_lc_report", "$__fern_lc_alloc_count", "$__fern_proc_exit_raw"} {
		if !strings.Contains(on, want) {
			t.Errorf("flag-on wat is missing %q", want)
		}
	}
}

func TestSelfHostWasmLeakcheckComponent(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm leakcheck component e2e")
	}
	for _, tc := range []struct {
		name, src            string
		exit                 int
		allocs, frees, bytes int64
		fromExit             bool
	}{
		{"returning", `function main(): i32 { let p: usize = __alloc(24); return 0; }`, 0, 1, 0, 24, false},
		{"exiting", `function main(): i32 { let p: usize = __alloc(24); exit(9); return 1; }`, 1, 1, 0, 24, true},
		{"printing", "import \"std/i32\";\nfunction main(): i32 { let s: string = \"a\" + 107.to_string(); print(s); return 0; }", 0, 5, 5, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			comp := filepath.Join(dir, "main.wasm")
			cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, src, comp)
			cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			run := exec.Command("wasmtime", "run", comp)
			var stderr bytes.Buffer
			run.Stderr = &stderr
			_ = run.Run()
			if got := run.ProcessState.ExitCode(); got != tc.exit {
				t.Fatalf("exit %d, want %d; stderr: %s", got, tc.exit, stderr.String())
			}
			if n := strings.Count(stderr.String(), "leakcheck: allocs="); n != 1 {
				t.Fatalf("%d census lines, want exactly 1: %q", n, stderr.String())
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr.String()), &allocs, &frees, &live); err != nil || allocs != tc.allocs || frees != tc.frees || live != tc.bytes {
				t.Errorf("census %q, want allocs=%d frees=%d live_bytes=%d", leakSummaryLine(stderr.String()), tc.allocs, tc.frees, tc.bytes)
			}
			if got := strings.HasSuffix(leakSummaryLine(stderr.String()), " from=exit"); got != tc.fromExit {
				t.Errorf("census %q says from=exit %v, want %v", leakSummaryLine(stderr.String()), got, tc.fromExit)
			}
		})
	}
}
