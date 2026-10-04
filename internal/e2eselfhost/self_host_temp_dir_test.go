package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSelfHostWasmTempDirName runs temp_dir on both wasm framings the self-host
// CLI builds: the default component and the preview-1 core module. Each makes
// `<prefix>-` and eight hex digits under the preopen, the name native's
// wasm temp_dir draws from the CSPRNG, and returns that preopen-relative path.
func TestSelfHostWasmTempDirName(t *testing.T) {
	requireWasmTools(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
    match (temp_dir("probe")) {
        Ok(p) => { print(p); return 0; },
        Err(e) => { return 1; }
    }
    return 1;
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	name := regexp.MustCompile(`^probe-[0-9a-f]{8}$`)
	for _, form := range [][]string{nil, {"-emit", "core-module"}} {
		label := "component"
		if form != nil {
			label = "core-module"
		}
		t.Run(label, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "main.wasm")
			args := append(append([]string{"-target", "wasm32-wasi"}, form...), "-o", out, src, cli.stdlib)
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI: %v\n%s", err, msg)
			}
			run := t.TempDir()
			got, err := exec.Command("wasmtime", "run", "--dir", run, out).Output()
			if err != nil {
				t.Fatalf("wasmtime run: %v", err)
			}
			made := strings.TrimSuffix(string(got), "\n")
			if !name.MatchString(made) {
				t.Errorf("temp_dir returned %q, want probe-<8 hex>", made)
			}
			if fi, err := os.Stat(filepath.Join(run, made)); err != nil || !fi.IsDir() {
				t.Errorf("%q is not a directory under the preopen (err = %v)", made, err)
			}
		})
	}
}

// TestSelfHostTempDirErrorNamesPrefix makes temp_dir fail with a prefix longer
// than a file name may be. The IoError names the prefix the caller passed, as
// the interpreter and every native backend report it, and not the path the
// self-host built from it: the x86-64 and arm64 runtime named the whole
// /tmp/<prefix>-<ns> path, and the wasm one the full <prefix>-<ns> name.
func TestSelfHostTempDirErrorNamesPrefix(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
    let prefix: string = "";
    let i: i32 = 0;
    while (i < 300) {
        prefix = prefix + "a";
        i = i + 1;
    }
    match (temp_dir(prefix)) {
        Ok(d) => { print("ok"); },
        Err(e) => {
            match (e) {
                Other(p, m) => { if (p == prefix) { print("prefix " + m); } else { print("path " + p); } },
                _ => { print("another variant"); }
            }
        }
    }
    return 0;
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "prefix File name too long\n"
	compile := func(t *testing.T, out string, args ...string) {
		t.Helper()
		cmd := runX86_64Bin(cli.runner, cli.bin, append(append(args, "-o", out), src, cli.stdlib)...)
		cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("self-host CLI: %v\n%s", err, msg)
		}
	}
	check := func(t *testing.T, cmd *exec.Cmd) {
		t.Helper()
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if string(got) != want {
			t.Errorf("stdout = %q, want %q", got, want)
		}
	}
	t.Run("x86-64", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "main")
		compile(t, bin, "-target", "x86-64-linux")
		check(t, runX86_64Bin(cli.runner, bin))
	})
	t.Run("arm64", func(t *testing.T) {
		qemu := arm64Runner(t)
		bin := filepath.Join(t.TempDir(), "main")
		compile(t, bin, "-target", "arm64-linux")
		check(t, runArm64Bin(qemu, bin))
	})
	for _, form := range [][]string{nil, {"-emit", "core-module"}} {
		label := "wasm component"
		if form != nil {
			label = "wasm core-module"
		}
		t.Run(label, func(t *testing.T) {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Skip("wasmtime not on PATH")
			}
			out := filepath.Join(t.TempDir(), "main.wasm")
			compile(t, out, append([]string{"-target", "wasm32-wasi"}, form...)...)
			check(t, exec.Command("wasmtime", "run", "--dir", t.TempDir(), out))
		})
	}
}
