package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostRuntimeKeys(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	const driver = `import "./asmcore";
function main(): i32 {
  let t: string = args()[1];
  print(asmcore.sysno(t, "read") + " " + asmcore.oflag(t, "wr_creat_trunc"));
  return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "runtime_key.fern"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "runtime_key.fern", "runtime_key")
	for _, tc := range []struct{ key, want string }{
		{"x86-64-linux", "0 577\n"},
		{"arm64-linux", "63 577\n"},
		{"arm64-darwin", "3 1537\n"},
	} {
		out, err := runX86_64Bin(runner, bin, tc.key).Output()
		if err != nil {
			t.Errorf("%s: %v", tc.key, err)
			continue
		}
		if string(out) != tc.want {
			t.Errorf("%s: got %q, want %q", tc.key, out, tc.want)
		}
	}
	for _, key := range []string{"x86_64", "arm64", "arm64-android", "wasm32-wasi"} {
		cmd := runX86_64Bin(runner, bin, key)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 3 {
			t.Errorf("%s: %v, want exit 3", key, err)
		}
		if !strings.Contains(stderr.String(), `"`+key+`", which is not a runtime key`) {
			t.Errorf("%s: stderr %q does not name the key", key, stderr.String())
		}
	}
}
