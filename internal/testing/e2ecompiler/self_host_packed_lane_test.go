package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostPackedLaneBounds(t *testing.T) {
	for _, target := range []string{e2eharness.HostSelfHostTarget(), e2eharness.TargetWasm32Wasi} {
		if target == "" {
			continue
		}
		if target == e2eharness.TargetWasm32Wasi {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				continue
			}
		}
		for _, width := range []int{32, 64} {
			for _, store := range []bool{false, true} {
				for _, offset := range []int{-1, 0, 16, 17, 2147483647} {
					name := fmt.Sprintf("%s/%d/store=%t/offset=%d", target, width, store, offset)
					t.Run(name, func(t *testing.T) {
						prefix := fmt.Sprintf("__f%dx%d_", width, 128/width)
						op := fmt.Sprintf("let v = %sload_bytes(xs, %d); return %sbitmask(v);", prefix, offset, prefix)
						if store {
							op = fmt.Sprintf("let out = %swith_bytes(xs, %d, %ssplat(1.0)); return out[0] as i32;", prefix, offset, prefix)
						}
						source := "function main(): i32 { let xs = __alloc_u8(32); " + op + " }"
						dir := t.TempDir()
						entry := filepath.Join(dir, "main.fern")
						bin := filepath.Join(dir, "program")
						if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
							t.Fatal(err)
						}
						cmd := e2eharness.SelfHostCompileCmd(t, target, entry, bin)
						cmd.Env = e2eharness.SelfHostChildEnv()
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("compile: %v\n%s", err, out)
						}
						run := exec.Command(bin)
						if target == e2eharness.TargetWasm32Wasi {
							run = e2eharness.RunWasmCore(t, bin)
						}
						out, err := run.CombinedOutput()
						valid := offset == 0 || offset == 16
						if valid && err != nil {
							t.Fatalf("valid access failed: %v\n%s", err, out)
						}
						if !valid && err == nil {
							t.Fatalf("access beyond the 16-byte window succeeded")
						}
					})
				}
			}
		}
	}
}
