//go:build linux

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The sandbox sets PR_SET_NO_NEW_PRIVS before its filter, so a setuid-root
// helper that a sandboxed program runs after it dropped to an ordinary
// user must stay that user. The same program without the sandbox is the
// control: there the helper regains root, which proves the helper works.
// The child inherits the filter, so the driver calls geteuid itself to put
// the helper's one extra system call on the allowlist.
func TestSeccompNoNewPrivsBlocksSetuidEscalation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("installing a setuid-root helper needs root")
	}
	whoami := buildSandboxed(t, `function main(): i32 { print(geteuid().to_string()); return 0; }`, false)
	// The helper must be reachable by the unprivileged user, so it lives
	// directly under /tmp rather than under the test's private directory.
	helperDir, err := os.MkdirTemp("/tmp", "fern-nnp-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(helperDir)
	if err := os.Chmod(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(helperDir, "whoami")
	image, err := os.ReadFile(whoami)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, image, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(helper, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(helper, 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`import "std/string";
function main(): i32 {
  match (setuid(65534i64)) {
    Ok(_) => { },
    Err(_) => { print("setuid failed"); return 2; }
  }
  let r: ProcessResult = subprocess(%q, [], "");
  print("self " + geteuid().to_string() + " helper " + r.stdout.trim_end() + " exit " + r.exit_code.to_string());
  return 0;
}`, helper)
	run := func(t *testing.T, bin string) string {
		t.Helper()
		out, err := exec.Command(bin).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", bin, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	control := run(t, buildSandboxed(t, src, false))
	if control != "self 65534 helper 0 exit 0" {
		t.Skipf("the control printed %q, so this host does not let a setuid-root helper regain root; the sandbox case cannot prove anything here", control)
	}
	if got := run(t, buildSandboxed(t, src, true)); got != "self 65534 helper 65534 exit 0" {
		t.Errorf("under the sandbox the helper printed %q, want \"self 65534 helper 65534 exit 0\": no_new_privs did not hold", got)
	}
}
