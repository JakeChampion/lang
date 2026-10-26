package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostUnsafeSyntax(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	sources := []string{"// Caller obligation.\nunsafe\n// Keep this proof.\nfunction answer(): i32 { unsafe { // Scope proof.\nreturn 42; } }", "struct Item {} trait Read { unsafe function read(self: Self): i32; } impl Read for Item { unsafe function read(self: Self): i32 { unsafe { return 42; } } }", "function unsafe(): i32 { let unsafe: i32 = 42; return unsafe; }"}
	for _, prefix := range []string{"", "pub ", "pub(package) ", "fip ", "fbip(2) ", "async ", "@inline\n"} {
		sources = append(sources, prefix+"unsafe function answer(): i32 { unsafe { return 42; } }")
	}
	for _, source := range sources {
		prog, err := parser.Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "unsafe.fern")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			out, err := exec.Command(cli, "-fmt", "-w", path).CombinedOutput()
			if err != nil {
				t.Fatalf("format: %v\n%s", err, out)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != printer.Format(prog) {
				t.Fatalf("format lost syntax\ngot:\n%s\nwant:\n%s", got, printer.Format(prog))
			}
		}
	}
}

func TestSelfHostUnsafeBlockScope(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	for _, source := range []string{
		"function main(): i32 { unsafe { let hidden: i32 = 42; } return hidden; }",
		"unsafe function main(): i32 { unsafe { return false; } }",
		"function take(own xs: i32[]): i32 { return xs[0]; } unsafe function f(own xs: i32[]): i32 { unsafe { let a: i32 = take(xs); return take(xs) + a; } }",
		"function main(): i32 { unsafe function inner(): i32 { return 42; } return inner(); }",
	} {
		path := filepath.Join(t.TempDir(), "bad.fern")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(cli, "-check", path, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "error[") {
			t.Fatalf("invalid program accepted: %v\n%s\n%s", err, out, source)
		}
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			source := "unsafe function answer(): i32 { unsafe { return 42; } } function main(): i32 { let value: i32 = 0; unsafe { let hidden: i32 = answer(); value = hidden; } print(value); return 0; }"
			bin := e2eharness.CompileSelfHostSource(t, target, source, nil)
			var cmd *exec.Cmd
			switch target {
			case e2eharness.TargetX86_64Linux:
				cmd = e2eharness.RunX86_64Bin(e2eharness.X86_64Runner(t), bin)
			case e2eharness.TargetArm64Linux:
				cmd = e2eharness.RunArm64Bin(e2eharness.Arm64Runner(t), bin)
			default:
				cmd = e2eharness.RunWasmCore(t, bin)
			}
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != "42\n" {
				t.Fatalf("block execution: %v\n%s", err, out)
			}
		})
	}
}
