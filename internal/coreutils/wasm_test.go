package coreutils

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// TestWasmSmoke runs a few utilities built for wasm32-wasi under wasmtime
// and holds them to the native build of the same source. It is not a GNU
// parity gate — the corpora are that — but the proof that the tree runs on
// the wasm target at all (#9070): argv and stdout reach the host, a stdio
// handle answers stat, a path with no preopened directory is an error
// rather than a trap, and exit codes survive wasi:cli/exit, which carries
// one bit — so a non-zero native status is compared as non-zero, not by
// value.
func TestWasmSmoke(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatalf("wasmtime is not on PATH: the wasm32-wasi smoke gate cannot run, and a SKIP here would report a target nobody exercised")
	}
	fern := e2eharness.BuildLangBinForInterp(t)
	root := repoRoot(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("x\ny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wasmDir := t.TempDir()
	built := map[string]string{}
	wasmBin := func(util string) string {
		if bin, ok := built[util]; ok {
			return bin
		}
		e2eharness.TrackFernSources(t, filepath.Join(root, "coreutils"), util+".fern")
		bin := filepath.Join(wasmDir, util+".wasm")
		cmd := exec.Command(fern, "-target", "wasm32-wasi", "-o", bin, filepath.Join(root, "coreutils", util+".fern"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile %s for wasm32-wasi: %v\n%s", util, err, out)
		}
		built[util] = bin
		return bin
	}

	cases := []struct {
		util  string
		args  []string
		stdin string
		// preopen mounts the working directory; without it the guest
		// has no directory at all, which the e2e suite covers.
		preopen bool
	}{
		{util: "true", args: []string{"--version"}},
		{util: "true", args: []string{"--", "--version"}},
		{util: "false"},
		{util: "echo", args: []string{"hello", "wasm"}},
		{util: "basename", args: []string{"a/b/c.txt", ".txt"}},
		{util: "cat", args: []string{"-n"}, stdin: "x\ny\n"},
		{util: "cat", args: []string{"in.txt", "-"}, stdin: "z\n", preopen: true},
		{util: "cat", args: []string{"nosuch"}, preopen: true},
		{util: "head", args: []string{"-n", "2"}, stdin: "a\nb\nc\n"},
		{util: "head", args: []string{"-n", "1", "in.txt"}, preopen: true},
		{util: "head", args: []string{"nosuch"}, preopen: true},
		{util: "head", args: []string{"--", "--version"}},
		{util: "wc", stdin: "a\nb\n"},
		{util: "wc", args: []string{"in.txt", "-"}, stdin: "q\n", preopen: true},
		{util: "wc", args: []string{"-l", "nosuch"}},
		// dd is the one utility here that WRITES a file, which on wasm
		// is a preopened directory's `path_open` and then the write
		// loop; its report lands on stderr, so the record counts are
		// compared as well as the bytes.
		{util: "dd", args: []string{"if=in.txt", "of=ddout", "bs=2", "status=noxfer"}, preopen: true},
		{util: "dd", args: []string{"bs=3", "status=noxfer"}, stdin: "abcdefg"},
		{util: "dd", args: []string{"if=nosuch", "status=noxfer"}, preopen: true},
	}
	for _, c := range cases {
		name := c.util + " " + strings.Join(c.args, " ")
		t.Run(name, func(t *testing.T) {
			inv := invocation{args: c.args, stdin: c.stdin, dir: dir}
			want := inv.run(t, fernBin(t, c.util), c.util)

			argv := []string{"run", "--argv0", c.util}
			if c.preopen {
				argv = append(argv, "--dir="+dir)
			}
			argv = append(append(argv, wasmBin(c.util)), c.args...)
			cmd := exec.Command(wasmtime, argv...)
			cmd.Dir = dir
			cmd.Env = append(baseEnv(), "HOME="+os.Getenv("HOME"))
			cmd.Stdin = strings.NewReader(c.stdin)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			exit := 0
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("wasmtime: %v", err)
			}
			if !bytes.Equal(want.stdout, stdout.Bytes()) {
				t.Errorf("stdout differs for %s %s\nnative: %s\n  wasm: %s", c.util, quoteArgs(c.args), quote(want.stdout), quote(stdout.Bytes()))
			}
			if !bytes.Equal(want.stderr, stderr.Bytes()) {
				t.Errorf("stderr differs for %s %s\nnative: %s\n  wasm: %s", c.util, quoteArgs(c.args), quote(want.stderr), quote(stderr.Bytes()))
			}
			if (want.exit != 0) != (exit != 0) || want.signal != "" {
				t.Errorf("status differs for %s %s: native %s, wasm exit %d", c.util, quoteArgs(c.args), want.how(), exit)
			}
		})
	}
}
