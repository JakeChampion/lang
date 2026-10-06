//go:build linux

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Reading a directory fails with EISDIR on every target (#11706). Every read
// path is covered: wasm opens a directory without complaint, as the natives
// do, so the failure only comes at the read.
const readDirProg = `function msg(e: IoError): string {
    match (e) {
        Other(_, m, _) => { return m; },
        _ => { return "another variant"; }
    }
    return "";
}

function main(): i32 {
    match (read_file("d")) { Ok(_) => { print("read_file ok"); }, Err(e) => { print("read_file " + msg(e)); } }
    match (read_file_bytes("d")) { Ok(_) => { print("read_file_bytes ok"); }, Err(e) => { print("read_file_bytes " + msg(e)); } }
    match (open_reader("d")) {
        Ok(r) => {
            match (r.read_chunk(16)) { Ok(_) => { print("read_chunk ok"); }, Err(e) => { print("read_chunk " + msg(e)); } }
            match (r.read_chunk_bytes(16)) { Ok(_) => { print("read_chunk_bytes ok"); }, Err(e) => { print("read_chunk_bytes " + msg(e)); } }
            match (r.close()) { Some(e) => { print("close " + msg(e)); }, None => {} }
        },
        Err(e) => { print("open_reader " + msg(e)); }
    }
    return 0;
}
`

const readDirWant = "read_file Is a directory\n" +
	"read_file_bytes Is a directory\n" +
	"read_chunk Is a directory\n" +
	"read_chunk_bytes Is a directory\n"

func TestReadDirectoryIsEisdirEveryTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(readDirProg), 0o644); err != nil {
		t.Fatal(err)
	}

	targets := []struct {
		name string
		cmd  func(t *testing.T) *exec.Cmd
	}{
		{"interpreter", func(t *testing.T) *exec.Cmd {
			return exec.Command(e2eharness.BuildLangBinForInterp(t), "-interp", srcPath)
		}},
		{"x86_64", func(t *testing.T) *exec.Cmd {
			bin, runner := e2eharness.CompileX86_64Bin(t, readDirProg)
			return e2eharness.RunX86_64Bin(runner, bin)
		}},
		{"arm64", func(t *testing.T) *exec.Cmd {
			bin, qemu := e2eharness.CompileArm64Bin(t, readDirProg)
			return e2eharness.RunArm64Bin(qemu, bin)
		}},
		{"wasm32-wasi", func(t *testing.T) *exec.Cmd {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Fatal("wasmtime not on PATH")
			}
			wasm := filepath.Join(t.TempDir(), "main.wasm")
			fern := e2eharness.BuildLangBinForInterp(t)
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", wasm, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
			}
			return exec.Command("wasmtime", "run", "--dir", dir, wasm)
		}},
		{"wasm-core", func(t *testing.T) *exec.Cmd {
			core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, readDirProg, nil)
			return exec.Command("wasmtime", "run", "--dir", dir, core)
		}},
		{"wasm-component", func(t *testing.T) *exec.Cmd {
			return exec.Command("wasmtime", "run", "--dir="+dir, buildComponent(t, readDirProg))
		}},
	}
	for _, tc := range targets {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd(t)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != readDirWant {
				t.Errorf("%v\noutput:\n%s\nwant:\n%s", err, out, readDirWant)
			}
		})
	}
}
