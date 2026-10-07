// `mounts()` (#9104) end to end on the Linux backends. The table is whatever
// the runner has mounted, so each compiled leg is compared against the
// interpreter's reading of the same table, and the root row is pinned to the
// device Go's own stat of `/` reports: a misparsed MAJ:MIN or a field taken
// one column off still prints a plausible table.
//
// The compiled legs run under the leak census and read the table several
// times, so a row, a field or the read buffer left behind on each call shows
// up as live bytes. Darwin's leg is the Mach-O test's `mounts` case; wasm has
// none, since `fsinfo` withholds the builtin there.
package e2e

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const mountsProbe = `import "std/i64";
function main(): i32 {
  let i: i32 = 0;
  while (i < 3) {
    match (mounts()) {
      Ok(ms) => { if (ms.len() == 0) { return 2; } },
      Err(_) => { return 3; }
    }
    i = i + 1;
  }
  match (mounts()) {
    Ok(ms) => {
      for m in ms {
        print(m.source + "\t" + m.target + "\t" + m.fstype + "\t" + m.dev.to_string());
      }
    },
    Err(_) => { return 1; }
  }
  return 0;
}
`

// checkMountsTable compares a compiled leg's table with the interpreter's and
// pins the root row's device to the host's stat of `/`.
func checkMountsTable(t *testing.T, got, stderr string, code int) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, got, stderr)
	}
	want, interpCode := runInterpExitCode(t, mountsProbe)
	if interpCode != 0 {
		t.Fatalf("interpreter exit = %d\n%s", interpCode, want)
	}
	if got != want {
		t.Errorf("table differs from the interpreter's\ncompiled:\n%s\ninterp:\n%s", got, want)
	}
	if _, _, live := parseLeakCheckLine(t, stderr); live != 0 {
		t.Errorf("live_bytes = %d after four reads of the table, want 0\n%s", live, stderr)
	}
	fi, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	rootDev := strconv.FormatUint(uint64(fi.Sys().(*syscall.Stat_t).Dev), 10)
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 4 && f[1] == "/" && f[3] == rootDev {
			return
		}
	}
	t.Errorf("no row mounts / with device %s\n%s", rootDev, got)
}

func TestX86_64Mounts(t *testing.T) {
	out, stderr, code := runLeakCheckX86_64(t, mountsProbe)
	checkMountsTable(t, out, stderr, code)
}

func TestArm64Mounts(t *testing.T) {
	out, stderr, code := runLeakCheckArm64(t, mountsProbe)
	checkMountsTable(t, out, stderr, code)
}

// Both wasm worlds refuse it: a preopen is a capability handle, not a mount,
// so there is no table to list.
func TestWASMMountsRefused(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 {
    match (mounts()) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
}
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted mounts; it has no mount table", target)
			continue
		}
		if vs[0].Builtin != "mounts" || vs[0].Capability != "fsinfo" {
			t.Errorf("%s refused %q on %q, want mounts on fsinfo", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
