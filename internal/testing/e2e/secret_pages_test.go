//go:build linux

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The program prints the lock state of two pages, the VmLck line before their
// last use and after their drop, the number of dontdump mappings they add, and
// PR_GET_DUMPABLE after secret.disable_dumps.
const secretPagesSrc = `import "core/cmp";
import "std/crypto/secret";
import "std/result";
import "std/string";

function vm_lck(): string {
  let Ok(status) = read_file("/proc/self/status") else { return "none"; };
  for line in status.split("\n") {
    if (line.starts_with("VmLck:")) {
      return line.replace("\t", " ");
    }
  }
  return "none";
}

function dontdump_mappings(): i32 {
  let Ok(smaps) = read_file("/proc/self/smaps") else { return -1; };
  let n = 0;
  for line in smaps.split("\n") {
    if (line.starts_with("VmFlags:") && (line + " ").contains(" dd ")) {
      n += 1;
    }
  }
  return n;
}

function hold(): i32 {
  let before = dontdump_mappings();
  let Ok(p) = secret.new(8192) else { return 2; };
  if (p.write(8000, [7u8, 9u8]).is_err()) { return 3; }
  let Ok(back) = p.read(8000, 2) else { return 4; };
  if (back != [7u8, 9u8]) { return 5; }
  print("during=" + vm_lck());
  print(f"dd={dontdump_mappings() - before}");
  print(f"locked={p.locked()} errno={p.lock_errno()}");
  p.wipe();
  return 0;
}

function main(): i32 {
  if (!secret.supported()) { return 1; }
  let code = hold();
  if (code != 0) { return code; }
  print("after=" + vm_lck());
  if (!secret.disable_dumps()) { return 7; }
  print(f"dumpable={prctl_query(3)}");
  return 0;
}
`

func secretPagesRun(t *testing.T, argv []string) map[string]string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v: %v\nstdout:\n%s\nstderr:\n%s", argv, err, stdout.String(), stderr.String())
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

func TestSelfHostSecretPages(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("secret pages run on x86-64 and arm64 Linux hosts")
	}
	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		t.Fatal("prlimit (util-linux) is necessary to run the program with no lockable memory")
	}
	host := e2eharness.HostSelfHostTarget()
	targets := []string{host}
	if runtime.GOARCH == "amd64" {
		targets = append(targets, "arm64-linux")
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var runner []string
			if target == "arm64-linux" {
				if qemu := e2eharness.Arm64Runner(t); qemu != "" {
					runner = []string{qemu}
				}
			}
			bin := e2eharness.CompileSelfHostSource(t, target, secretPagesSrc, nil)
			argv := append(append([]string{}, runner...), bin)
			got := secretPagesRun(t, argv)
			if got["locked"] != "true errno=0" {
				t.Errorf("locked=%q, want the pages locked", got["locked"])
			}
			if got["dumpable"] != "0" {
				t.Errorf("dumpable=%q after disable_dumps, want 0", got["dumpable"])
			}
			if len(runner) == 0 {
				// qemu-user reports its own mappings, so only a native run reads them.
				if got["dd"] != "1" {
					t.Errorf("dd=%q, want one more mapping with the dontdump flag", got["dd"])
				}
				if lockedKB(got["during"]) < 8 {
					t.Errorf("during=%q, want at least 8 kB locked", got["during"])
				}
				if lockedKB(got["after"]) != lockedKB(got["during"])-8 {
					t.Errorf("after=%q during=%q, want the drop to unlock 8 kB", got["after"], got["during"])
				}
			}

			// With no lockable memory and no CAP_IPC_LOCK the kernel refuses
			// the lock, and the pages stay usable.
			limited := append([]string{prlimit, "--memlock=0:0", "--"}, argv...)
			if os.Geteuid() == 0 {
				setpriv, err := exec.LookPath("setpriv")
				if err != nil {
					t.Fatal("setpriv (util-linux) is necessary to drop CAP_IPC_LOCK from a root run")
				}
				limited = append([]string{setpriv, "--bounding-set=-ipc_lock", "--"}, limited...)
			}
			refused := secretPagesRun(t, limited)
			if refused["locked"] != "false errno=1" && refused["locked"] != "false errno=12" {
				t.Errorf("locked=%q under a zero memlock limit, want false with EPERM or ENOMEM", refused["locked"])
			}
		})
	}
}

func lockedKB(line string) int {
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != "VmLck:" || f[2] != "kB" {
		return -1
	}
	n := 0
	for _, c := range f[1] {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func TestSecretPagesRefusedOffNativeHosts(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { unsafe { return secret_map(4096i64) as i32; } }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http", "arm64-freestanding", "x86-64-freestanding"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 || vs[0].Builtin != "secret_map" || vs[0].Capability != "rlimit" {
			t.Errorf("%s: violations %v, want secret_map refused on the rlimit capability", target, vs)
		}
	}
}

// The Go oracle serves secret pages from its raw arena and cannot lock them.
func TestInterpSecretPages(t *testing.T) {
	src := `import "core/cmp";
import "std/crypto/secret";
import "std/result";

function main(): i32 {
  let Ok(p) = secret.new(16) else { return 2; };
  if (p.write(14, [5u8, 6u8]).is_err()) { return 3; }
  if (p.write(15, [5u8, 6u8]).is_ok()) { return 4; }
  let Ok(back) = p.read(13, 3) else { return 5; };
  if (back != [0u8, 5u8, 6u8]) { return 6; }
  if (p.locked() || p.lock_errno() != 38) { return 7; }
  p.wipe();
  let Ok(zero) = p.read(14, 2) else { return 8; };
  if (zero != [0u8, 0u8]) { return 9; }
  return 0;
}
`
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	checkStatuses(t, exec.Command(buildLangBinForInterp(t), "-interp", p))
}
