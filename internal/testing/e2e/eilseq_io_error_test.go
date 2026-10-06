//go:build linux

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The path is relative because wasm resolves every path against its preopen.
const eilseqProg = `function main(): i32 {
    match (remove_file("victim.txt")) {
        Ok(_) => { print("removed"); return 1; },
        Err(e) => {
            match (e) {
                InvalidUtf8(p) => { print("InvalidUtf8 " + p); return 0; },
                Other(_, m, _) => { print("Other " + m); return 2; },
                _ => { print("another variant"); return 3; }
            }
        }
    }
}
`

// EILSEQ is IoError.InvalidUtf8 on every target (#11707). No unprivileged
// syscall reliably answers EILSEQ, so unlinkat is made to, under the seccomp
// launcher.
func TestEilseqIsInvalidUtf8EveryTarget(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	src := eilseqProg
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "InvalidUtf8 victim.txt"

	targets := []struct {
		name string
		argv func(t *testing.T) []string
	}{
		{"interpreter", func(t *testing.T) []string {
			return []string{e2eharness.BuildLangBinForInterp(t), "-interp", srcPath}
		}},
		{"x86_64", func(t *testing.T) []string {
			bin, runner := e2eharness.CompileX86_64Bin(t, src)
			return e2eharness.RunX86_64Bin(runner, bin).Args
		}},
		{"arm64", func(t *testing.T) []string {
			bin, qemu := e2eharness.CompileArm64Bin(t, src)
			return e2eharness.RunArm64Bin(qemu, bin).Args
		}},
		{"wasm32-wasi", func(t *testing.T) []string {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Fatal("wasmtime not on PATH")
			}
			wasm := filepath.Join(t.TempDir(), "main.wasm")
			fern := e2eharness.BuildLangBinForInterp(t)
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", wasm, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
			}
			return []string{"wasmtime", "run", "--dir", dir, wasm}
		}},
		{"wasm-core", func(t *testing.T) []string {
			core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, src, nil)
			return []string{"wasmtime", "run", "--dir", dir, core}
		}},
		{"wasm-component", func(t *testing.T) []string {
			return []string{"wasmtime", "run", "--dir=" + dir, buildComponent(t, src)}
		}},
	}
	for _, tc := range targets {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := underSeccompErrno(t, dir, syscall.EILSEQ, []uintptr{syscall.SYS_UNLINKAT}, tc.argv(t)...).CombinedOutput()
			if err != nil || !strings.Contains(string(out), want) {
				t.Errorf("%v\noutput %q, want it to contain %q", err, out, want)
			}
			if _, err := os.Stat(victim); err != nil {
				t.Errorf("the filter did not hold: %v", err)
			}
		})
	}
}
