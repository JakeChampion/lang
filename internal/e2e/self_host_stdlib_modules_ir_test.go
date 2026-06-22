package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real stdlib MODULES on the self-host IR path. Several self-host tests
// historically INLINED stdlib aggregate types (HeaderMap → "Headers",
// BytesWriter → "BW", Stream → "Buf") because the importless single-program
// driver could not `import "std/…"`. With stdlib loading + the (default-on)
// treeshake prune, the self-hosted compiler now compiles programs that import
// those REAL modules and use their public API, routing IR and matching the
// native interpreter. Drives the self-hosted x86-64 loader (asm_load_run) with
// the repo's real stdlib as the root — proof the inlining workarounds are no
// longer required.
var stdlibModuleCases = []struct {
	name string
	src  string
}{
	// std/headers HeaderMap: case-insensitive multimap (two parallel string[]
	// fields), functional struct-spread `append`, `get` → Option[string], `len`.
	{"headers", `import "std/headers";
function main(): i32 {
    var h = headers.header_map_new();
    h = h.append("Content-Type", "text/html");
    h = h.append("X-Count", "7");
    match (h.get("Content-Type")) {
        Some(v) => { if (v == "text/html") { return h.len() * 21; } return 0; },
        None => { return 0; }
    }
}`},
	// std/io_buffered BytesWriter: u8[]-backed builder, functional write_string,
	// len. "hello" + "!" = 6 bytes * 7 = 42.
	{"bytes-writer", `import "std/io_buffered";
function main(): i32 {
    var w = io_buffered.bytes_writer_new();
    w = w.write_string("hello");
    w = w.write_string("!");
    return w.len() * 7;
}`},
	// std/stream Stream: u8[] + cursor reader. len("hello world") = 11 * 3 = 33.
	{"stream", `import "std/stream";
function main(): i32 {
    var s = stream.stream_from_string("hello world");
    return s.len() * 3;
}`},
}

func TestSelfHostStdlibModulesIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range stdlibModuleCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "sm_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "sm_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
