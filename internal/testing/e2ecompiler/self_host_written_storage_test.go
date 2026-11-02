package e2ecompiler

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWrittenStoragePaths(t *testing.T) {
	h := selfHostCLIForHost(t)
	targets := append([]ssaBackendTarget{}, h.targets...)
	if wasmtime, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{wasmtime, "run", "-S", "inherit-network", "--env", "FERN_LEAKCHECK=1"}})
	} else {
		t.Run("wasm32-wasi", func(t *testing.T) { t.Skip("requires wasmtime") })
	}
	modes := []string{"", "record", "method", "tuple", "variant", "array", "return", "chain", "indirect-return", "cell", "cell-set", "cell-argument", "closure", "indirect-argument"}
	for _, target := range targets {
		t.Run(target.target, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "written-storage")
			entry := e2eharness.RepoPath("conformance", "cases", "raw_storage_paths_keep_literals_fresh", "main.fern")
			build := exec.Command(h.cli, "-target", target.target, "-sanitize", "-o", binary, entry, h.stdlib)
			build.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("compile written storage: %v\n%s", err, out)
			}
			for _, mode := range modes {
				name := mode
				if name == "" {
					name = "all"
				}
				t.Run(name, func(t *testing.T) {
					command := append(append([]string{}, target.runner...), binary)
					if mode != "" {
						command = append(command, mode)
					}
					run := exec.Command(command[0], command[1:]...)
					run.Env = childEnv("FERN_LEAKCHECK=1")
					var stderr bytes.Buffer
					run.Stderr = &stderr
					out, err := run.Output()
					if err != nil || string(out) != "ok\n" {
						t.Fatalf("written storage: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
					}
					assertBalancedCensus(t, stderr.String())
					if strings.Contains(stderr.String(), "allocs=0 ") {
						t.Fatal("written storage did not record allocation events")
					}
				})
			}
		})
	}
}

func TestSelfHostReadOnlyStorageStaysPooled(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)
	for _, tc := range []struct{ name, source, body string }{
		{"scalar-return", "@noinline function make(): i32[] { return [0, 0]; } function main(): i32 { return make()[0]; }", "make"},
		{"byte-return", "@noinline function make(): u8[] { return [0u8, 0u8]; } function main(): i32 { return make()[0] as i32; }", "make"},
		{"record-argument", "struct Holder { bytes: u8[] } @noinline function peek(h: Holder): i32 { return h.bytes[0] as i32; } function main(): i32 { return peek(Holder { bytes: [0u8, 0u8] }); }", "main"},
		{"cell-store", "@noinline function put(c: Cell[u8[]]): void { c.set([0u8, 0u8]); } function main(): i32 { let c: Cell[u8[]] = cell_new([]); put(c); return c.get()[0] as i32; }", "put"},
		{"empty-raw-array", "@noinline function touch(a: u8[]): i32 { unsafe { __arr_set_len(a, 0); } return a.len(); } function main(): i32 { let a: u8[] = []; return touch(a); }", "main"},
		{"empty-raw-record", "struct Holder { bytes: u8[] } @noinline function touch(h: Holder): i32 { unsafe { __arr_set_len(h.bytes, 0); } return h.bytes.len(); } function main(): i32 { return touch(Holder { bytes: [] }); }", "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.source)
			body := selfHostFnBody(t, []byte(asm), tc.body)
			if !strings.Contains(body, ".K") || strings.Contains(body, "call __fern_arr_box") || strings.Contains(body, "call __fn___fern_arr_slice_u8") {
				t.Fatalf("read-only array leaves the constant pool:\n%s", body)
			}
			if code, out := cli.runX86(t, asm); code != 0 {
				t.Fatalf("read-only storage: exit %d, want 0: %s", code, out)
			}
		})
	}
}
