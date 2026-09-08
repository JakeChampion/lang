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
	checkTypedSSAExecutable(t, bin, `
function main(): i32 { var items = [[17i32, 29i32]]; return get(items[0].append(41i32)); }
function get(items: i32[]): i32 { return items[2]; }
`)
}

func TestTypedSSASourceLoops(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	checkTypedSSAExecutable(t, bin, `
function main(): i32 {
  var items = [[17i32, 29i32]]; var i = 0i32;
  while (i < 2i32) { items = items.append([41i32]); i = i + 1i32; }
  return get(items[2]);
}
function get(items: i32[]): i32 { return items[0]; }
`)
}

func TestTypedSSAExpressionFlow(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	for _, tc := range []struct{ name, source string }{
		{"short-circuit-join", `function main(): i32 { return choose(false); }
function choose(flag: boolean): i32 {
  var items = if (flag && fault()) { [7i32] } else { [41i32] }; return items[0];
}
function fault(): boolean { var missing: boolean[] = []; return missing[0]; }`},
		{"element-return", `function main(): i32 { return choose(true); }
function choose(flag: boolean): i32 {
  var items = [[17i32], if (flag) { return 41i32; } else { [2i32] }]; return items[1][0];
}`},
	} {
		t.Run(tc.name, func(t *testing.T) { checkTypedSSAExecutable(t, bin, tc.source) })
	}
}

func TestTypedSSAMatchFlow(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	checkTypedSSAExecutable(t, bin, `
function main(): i32 { return choose(1i32); }
function choose(tag: i32): i32 {
  var items = [17i32];
  var selected = match (tag) {
    n @ 1i32 when { items = [41i32]; false } => [n],
    1i32 => items,
    _ => [29i32]
  };
  return selected[0];
}
`)
}

func TestTypedSSATupleMatchFlow(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	checkTypedSSAExecutable(t, bin, `
function main(): i32 {
  var pair = (([41i32], true), 1i32);
  var child = match (pair) {
    ((_, false), _) => [17i32],
    ((items, _), _) when { pair = (([29i32], false), 0i32); false } => items,
    ((items, _), _) => items
  };
  var i = 0i32; while (i < 64i32) { var churn = (([7i32], true), 2i32); i = i + 1i32; }
  return child[0];
}
`)
}

func checkTypedSSAExecutable(t *testing.T, bin, source string) {
	t.Helper()
	entry := writeFern(t, source)
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
		{"division", `function main(): i32 { var x = 4i32; return x / 2i32; }`, "unsupported scalar binary contract"},
		{"wide-arithmetic", `function main(): i32 { var x = 1i64; var y = x + 1i64; return 0; }`, "scalar binary requires"},
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
