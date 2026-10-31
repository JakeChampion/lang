package e2ecompiler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func buildCheckerModloadDriverX86(t *testing.T) (gcc string, runner []string, driverBin string) {
	t.Helper()
	gcc, runner = x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_modload_run.fern")
	return gcc, runner, buildSelfHostBin(t, gcc, dir, "drivers/checker_modload_run.fern", "ckdriver")
}

func checkSourceModload(t *testing.T, runner []string, driverBin, entrySrc string) string {
	t.Helper()
	_, srcs, err := modload.LoadSource(entrySrc)
	if err != nil {
		t.Fatalf("modload.LoadSource: %v", err)
	}
	return checkLoadedModload(t, runner, driverBin, "/__fern_source__/main.fern", entrySrc, srcs)
}

func checkFileModload(t *testing.T, runner []string, driverBin, entryPath string) string {
	t.Helper()
	_, srcs, err := modload.Load(entryPath)
	if err != nil {
		t.Fatalf("modload.Load: %v", err)
	}
	entrySrc, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("read %s: %v", entryPath, err)
	}
	return checkLoadedModload(t, runner, driverBin, entryPath, string(entrySrc), srcs)
}

func checkLoadedModload(t *testing.T, runner []string, driverBin, entryPath, entrySrc string, srcs map[string]string) string {
	t.Helper()
	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../../compiler/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	entryDir := filepath.Dir(entryPath)
	for p, src := range srcs {
		if p == entryPath {
			continue
		}
		name, ok := strings.CutPrefix(p, "stdlib://")
		if !ok {
			rel, err := filepath.Rel(entryDir, p)
			if err != nil || strings.HasPrefix(rel, "..") {
				t.Fatalf("module %s is not under the entry's directory %s", p, entryDir)
			}
			name = rel
		}
		dst := filepath.Join(progDir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"), []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, filepath.Join(progDir, "main.fern"))
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin, filepath.Join(progDir, "main.fern"))...)
	}
	return runCheckerDriver(t, cmd, "checkSourceModload")
}

func checkerDriverFault(code int, stderr []byte) string {
	if code == 0 || code == 1 {
		return ""
	}
	what := fmt.Sprintf("exited %d", code)
	switch {
	case code < 0:
		what = "died on a signal"
	case code == 2 || code == 3:
		what = fmt.Sprintf("bailed with its own failure code %d (load / check path)", code)
	}
	msg := "checker driver " + what + " rather than answering (0 = clean, 1 = diagnostics)"
	if s := strings.TrimSpace(string(stderr)); s != "" {
		msg += "\nstderr: " + s
	}
	return msg
}

func runCheckerDriver(t *testing.T, cmd *exec.Cmd, label string) string {
	t.Helper()
	out, err := cmd.Output()
	var stderr []byte
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		stderr = ee.Stderr
	}
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if fault := checkerDriverFault(code, stderr); fault != "" {
		t.Fatalf("%s: %s", label, fault)
	}
	return string(out)
}

func TestSelfHostCheckerModloadX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../../compiler/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "a.fern"),
		[]byte("pub function helper(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write a.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"),
		[]byte("import \"./a\";\nfunction main(): i32 { let x: i32 = a.helper(); if (x) { return 1; } return 0; }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, filepath.Join(progDir, "main.fern"))
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin, filepath.Join(progDir, "main.fern"))...)
	}
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "E008") {
		t.Errorf("checker driver: want an E008 diagnostic, got codes %q", string(out))
	}
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("checker driver exited %d, want 1 (diagnostics emitted)", code)
	}
}

func TestSelfHostCheckerModloadEmptyImplMangledX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../../compiler/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	files := map[string]string{
		"builtins.fern": string(bsrc),
		"num.fern": "pub struct Num { v: i32 }\n" +
			"pub function (n: Num) to_string(): string { return \"n\"; }\n",
		"cross.fern": "import \"./num\";\n" +
			"pub trait Show {\n    function to_string(self: Self): string;\n}\n" +
			"pub struct Tag { v: i32 }\n" +
			"pub function (t: Tag) to_string(): string { return \"t\"; }\n" +
			"impl Show for Tag { }\n" +
			"impl Show for num.Num { }\n",
		"main.fern": "import \"./num\";\nimport \"./cross\";\n" +
			"function main(): i32 { let n: num.Num = num.Num { v: 1 }; return n.v; }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, filepath.Join(progDir, "main.fern"))
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin, filepath.Join(progDir, "main.fern"))...)
	}
	out, _ := cmd.Output()
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("checker driver: want no diagnostics on a program whose empty impls are\nsatisfied by inherent methods, got codes %q", got)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("checker driver exited %d, want 0 (no diagnostics)", code)
	}
}

func TestSelfHostCheckerModloadTraitSigMangledX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)
	bsrc, err := os.ReadFile("../../../compiler/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	other := "pub struct Item { n: i32 }\npub enum Fault { Bad, Worse }\n"
	trait := "import \"./other\";\n" +
		"pub struct Local { n: i32 }\n" +
		"pub trait Tr {\n" +
		"    function a(self: Self, x: other.Item): i32;\n" +
		"    function b(self: Self, x: i32): Result[i32, other.Fault];\n" +
		"    function c(self: Self, x: other.Item[]): i32;\n" +
		"    function d(self: Self, x: i32): Local;\n" +
		"}\n" +
		"pub struct Impl {}\n"
	matching := "impl Tr for Impl {\n" +
		"    function a(self: Impl, x: other.Item): i32 { return x.n; }\n" +
		"    function b(self: Impl, x: i32): Result[i32, other.Fault] { return Ok(x); }\n" +
		"    function c(self: Impl, x: other.Item[]): i32 { return x.len(); }\n" +
		"    function d(self: Impl, x: i32): Local { return Local { n: x }; }\n" +
		"}\n"
	mismatched := "impl Tr for Impl {\n" +
		"    function a(self: Impl, x: other.Item): i32 { return x.n; }\n" +
		"    function b(self: Impl, x: i32): Result[i32, other.Fault] { return Ok(x); }\n" +
		"    function c(self: Impl, x: other.Item[]): i32 { return x.len(); }\n" +
		"    function d(self: Impl, x: i32): other.Item { return other.Item { n: x }; }\n" +
		"}\n"
	main := "import \"./lib\";\nimport \"./other\";\n" +
		"function main(): i32 { let i: lib.Impl = lib.Impl {}; return i.a(other.Item { n: 0 }); }\n"
	for _, tc := range []struct {
		name, impl string
		want       []string
	}{
		{"matching", matching, nil},
		{"mismatched", mismatched, []string{"E021"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			for name, src := range map[string]string{
				"builtins.fern": string(bsrc),
				"other.fern":    other,
				"lib.fern":      trait + tc.impl,
				"main.fern":     main,
			} {
				if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, filepath.Join(progDir, "main.fern"))
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin, filepath.Join(progDir, "main.fern"))...)
			}
			out, _ := cmd.Output()
			if got := driverCodes(string(out)); !equalStrings(got, tc.want) {
				t.Errorf("checker driver codes = %q, want %q\noutput:\n%s", got, tc.want, out)
			}
		})
	}
}
