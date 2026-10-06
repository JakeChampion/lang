//go:build linux

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The paths are relative because wasm resolves every path against its preopen.
const enotsupProg = `import "std/errno";
import "std/i32";

function show(label: string, r: Result[void, IoError]): void {
    match (r) {
        Ok(_) => { print(label + " succeeded"); },
        Err(e) => {
            match (e) {
                Unsupported => { print(label + " Unsupported"); },
                Other(p, m, n) => { print(label + " Other " + p + " " + m + " " + n.to_string() + " " + errno.of(e).to_string()); },
                _ => { print(label + " another variant"); }
            }
        }
    }
}

function main(): i32 {
    show("remove_file", remove_file("victim.txt"));
    show("create_dir", create_dir("fresh", 493));
    return 0;
}
`

const enotsupWant = "remove_file Other victim.txt Operation not supported 95 95\n" +
	"create_dir Other fresh Operation not supported 95 95\n"

// A host's ENOTSUP is IoError.Other carrying it on every target, where
// Unsupported is the target refusing an operation it does not offer (#11712).
// unlinkat and mkdirat answer ENOTSUP under the seccomp launcher; the wasm
// host hands it on as preview 1's ENOTSUP or preview 2's `unsupported`.
// wasmtime runs with its cache off, which it would otherwise mkdirat into
// being under the same filter.
func TestEnotsupIsOtherEveryTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "victim.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(enotsupProg), 0o644); err != nil {
		t.Fatal(err)
	}
	leakcheck := []string{"FERN_LEAKCHECK=1"}

	targets := []struct {
		name   string
		census bool
		argv   func(t *testing.T) []string
	}{
		{"interpreter", false, func(t *testing.T) []string {
			return []string{e2eharness.BuildLangBinForInterp(t), "-interp", srcPath}
		}},
		{"x86_64", true, func(t *testing.T) []string {
			runner := e2eharness.X86_64Runner(t)
			bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetX86_64Linux, srcPath, leakcheck)
			return e2eharness.RunX86_64Bin(runner, bin).Args
		}},
		{"arm64", true, func(t *testing.T) []string {
			qemu := e2eharness.Arm64Runner(t)
			bin := e2eharness.CompileSelfHostFile(t, e2eharness.TargetArm64Linux, srcPath, leakcheck)
			return e2eharness.RunArm64Bin(qemu, bin).Args
		}},
		{"wasm-preview1", true, func(t *testing.T) []string {
			core := e2eharness.CompileSelfHostFile(t, e2eharness.TargetWasm32Wasi, srcPath, leakcheck)
			return []string{e2eharness.Wasmtime(t), "run", "-C", "cache=n", "--dir=.", core}
		}},
		{"wasm-component", true, func(t *testing.T) []string {
			component := filepath.Join(t.TempDir(), "main.wasm")
			cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, srcPath, component)
			cmd.Env = e2eharness.SelfHostChildEnv(leakcheck...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile component: %v\n%s", err, out)
			}
			return []string{e2eharness.Wasmtime(t), "run", "-C", "cache=n", "--dir=.", component}
		}},
	}
	for _, tc := range targets {
		t.Run(tc.name, func(t *testing.T) {
			argv := tc.argv(t)
			var stdout, stderr bytes.Buffer
			cmd := underSeccompErrno(t, dir, syscall.ENOTSUP, []uintptr{syscall.SYS_UNLINKAT, syscall.SYS_MKDIRAT}, argv...)
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if got := stdout.String(); got != enotsupWant {
				t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", got, enotsupWant, stderr.String())
			}
			if tc.census {
				e2eharness.CheckLeakcheckBalanced(t, stderr.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "victim.txt")); err != nil {
				t.Errorf("the filter did not hold: %v", err)
			}
		})
	}
}
