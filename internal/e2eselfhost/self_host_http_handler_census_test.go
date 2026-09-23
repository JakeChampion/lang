package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostHTTPHandlerCensus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux native targets")
	}
	checkSelfHostHTTPHandlerCensus(t, []string{"x86-64-linux", "arm64-linux"})
}

func TestSelfHostArm64DarwinHTTPHandlerCensus(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkSelfHostHTTPHandlerCensus(t, []string{"arm64-darwin"})
}

func checkSelfHostHTTPHandlerCensus(t *testing.T, targets []string) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build compiler: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "server.fern")
	if err := os.WriteFile(src, []byte(e2eharness.HTTPHandlerCensusSource(t, "../..", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var run func(string) *exec.Cmd
			switch target {
			case "arm64-darwin":
				run = func(p string) *exec.Cmd { return exec.Command(p) }
			case "arm64-linux":
				_, q := arm64Tooling(t)
				run = func(p string) *exec.Cmd { return runArm64Bin(q, p) }
			default:
				_, q := x86_64Tooling(t)
				run = func(p string) *exec.Cmd { return runX86_64Bin(q, p) }
			}
			bin := filepath.Join(t.TempDir(), "server")
			cmd := exec.Command(driver, "-target", target, "-o", bin, src, stdlib)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1", "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1")
			report, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("build: %v\n%s", err, report)
			}
			requireCompleteHTTPSemanticLowering(t, report)
			out := e2eharness.RunHTTPHandlerCensus(t, run(bin), 32)
			allocs, frees, live := leakSummaryOf(t, "HTTP handler", out)
			t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
			if allocs != frees || live != 0 {
				t.Fatal("bounded HTTP handler leaked")
			}
		})
	}
}

func requireCompleteHTTPSemanticLowering(t *testing.T, report []byte) {
	t.Helper()
	// Correct responses alone could conceal an AST fallback and its ownership.
	rows := regexp.MustCompile(`FERN_SEM_IR: module: produced ([0-9]+) of ([0-9]+) declarations and ([0-9]+) of ([0-9]+) instances`).FindAllStringSubmatch(string(report), -1)
	if len(rows) == 0 || strings.Contains(string(report), "refused") || strings.Contains(string(report), "the AST lowering stands") {
		t.Fatalf("HTTP fixture must use semantic ownership: %s", report)
	}
	for _, row := range rows {
		if row[1] == "0" || row[1] != row[2] || row[3] != row[4] {
			t.Fatalf("HTTP fixture partially fell back: %s", report)
		}
	}
}
