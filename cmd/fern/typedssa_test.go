package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTypedSSACLICompilesThroughSemanticOwnership(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, `
function main(): i32 { var items = [[17i32, 29i32]]; return get(items[0].append(41i32)); }
function get(items: i32[]): i32 { return items[2]; }
`)
	asm, err := exec.Command(bin, "-backend", "typed-ssa", "-target", "arm64-linux", entry).CombinedOutput()
	if err != nil {
		t.Fatalf("typed-ssa compile: %v\n%s", err, asm)
	}
	for _, want := range []string{"_start:", "__semir_fn_", "__semir_helper_"} {
		if !strings.Contains(string(asm), want) {
			t.Fatalf("typed pipeline output missing %q", want)
		}
	}
	output := filepath.Join(t.TempDir(), "pilot")
	if msg, err := exec.Command(bin, "-backend", "typed-ssa", "-target", "arm64-linux", "-o", output, entry).CombinedOutput(); err != nil {
		t.Fatalf("typed-ssa link: %v\n%s", err, msg)
	}
	bytes, err := os.ReadFile(output)
	if err != nil || len(bytes) < 4 || string(bytes[:4]) != "\x7fELF" {
		t.Fatalf("expected linked ELF: %v", err)
	}
	t.Run("execution", func(t *testing.T) {
		launcher := ""
		if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
			for _, name := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
				if path, err := exec.LookPath(name); err == nil {
					launcher = path
					break
				}
			}
			if launcher == "" {
				if os.Getenv("FERN_REQUIRE_ARM64_SSA_DIFF") != "" {
					t.Fatal("required ARM64 execution unavailable")
				}
				t.Skip("ARM64 Linux or qemu-aarch64 required for execution")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, output)
		if launcher != "" {
			cmd = exec.CommandContext(ctx, launcher, output)
		}
		msg, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 41 {
			t.Fatalf("compiled pilot returned %v, want 41: %s", err, msg)
		}
	})
}

func TestTypedSSACLIRejectsUnsupportedConstructs(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	for _, tc := range []struct{ name, source, want string }{
		{"loop", `function main(): i32 { var x = 0; while (x < 2) { x = x + 1; } return x; }`, "unsupported statement"},
		{"mutation", `function main(): i32 { var x = 1; x = 2; return x; }`, "unsupported statement"},
		{"unresolved-literal-metadata", `function main(): i32 { var items = [[17, 29]]; return items[0][1]; }`, "unresolved or unsupported semantic type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := writeFern(t, tc.source)
			out, err := exec.Command(bin, "-backend", "typed-ssa", "-target", "arm64-linux", entry).CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("unsupported construct silently fell back: %v\n%s", err, out)
			}
		})
	}
}

func TestTypedSSACLIRejectsUnsupportedOptions(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 { return 0; }")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-target", "x86-64-linux"}, "not available for -target"},
		{[]string{"-target", "arm64-linux", "-run"}, "executable only"},
		{[]string{"-target", "arm64-linux", "-sanitize"}, "does not implement"},
		{[]string{"-target", "arm64-linux", "-cover"}, "does not implement"},
	} {
		args := append([]string{"-backend", "typed-ssa"}, tc.args...)
		args = append(args, entry)
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), tc.want) {
			t.Fatalf("unsupported options %v: %v\n%s", tc.args, err, out)
		}
	}
}
