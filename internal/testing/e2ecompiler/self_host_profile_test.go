package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A profile is a flag set, every native executable carries a build
// manifest, and a program can read its own status at run time.
const profileSrc = `import "std/mitigations";

function main(): i32 {
  for row in mitigations.manifest() {
    print(row.line());
  }
  return 0;
}
`

func TestSelfHostProfiles(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(profileSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	fern := func(args ...string) (string, string, int) {
		t.Helper()
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}
	profiles := []struct {
		name                string
		harden, pie, san, g string
	}{
		{"safe", "no", "no", "no", "no"},
		{"hardened", "yes", "yes", "no", "no"},
		{"diagnostic", "no", "no", "yes", "yes"},
		{"minimal", "no", "no", "no", "no"},
	}
	for _, tc := range []struct{ target string }{{"x86-64-linux"}, {"arm64-linux"}} {
		t.Run(tc.target, func(t *testing.T) {
			for _, p := range profiles {
				t.Run(p.name, func(t *testing.T) {
					bin := filepath.Join(dir, tc.target+"-"+p.name)
					if _, errout, code := fern("-profile", p.name, "-target", tc.target, "-o", bin, src, cli.stdlib); code != 0 {
						t.Fatalf("build: exit %d\n%s", code, errout)
					}
					out, errout, code := fern("-manifest", bin)
					if code != 0 {
						t.Fatalf("-manifest: exit %d\n%s", code, errout)
					}
					want := []string{"target: " + tc.target, "profile: " + p.name, "harden: " + p.harden, "pie: " + p.pie, "sanitize: " + p.san, "shared: no", "heap_limit: 17179869184"}
					if p.name == "minimal" {
						want = append(want, "size: yes", "backtrace: no")
					} else {
						want = append(want, "size: no", "backtrace: yes")
					}
					for _, line := range want {
						if !strings.Contains(out, line+"\n") {
							t.Errorf("the manifest lacks %q:\n%s", line, out)
						}
					}
					var c *exec.Cmd
					if tc.target == "arm64-linux" {
						c = runArm64Bin(qemu, bin)
					} else {
						c = runX86_64Bin(cli.runner, bin)
					}
					run, err := c.CombinedOutput()
					if err != nil {
						t.Fatalf("run: %v\n%s", err, run)
					}
					rows := map[string]string{}
					for _, line := range strings.Split(strings.TrimSpace(string(run)), "\n") {
						name, rest, _ := strings.Cut(line, " ")
						rows[name] = rest
					}
					for name, cell := range map[string]string{"canary": "compiled=" + p.harden, "cfi": "compiled=" + p.harden, "pie": "compiled=" + p.pie, "sanitizer": "compiled=" + p.san, "nx-stack": "compiled=yes supported=yes requested=yes enabled=yes"} {
						if !strings.Contains(" "+rows[name]+" ", " "+cell+" ") && !strings.HasPrefix(rows[name], cell) {
							t.Errorf("row %s = %q, want %q", name, rows[name], cell)
						}
					}
					hardware := []string{"shadow-stack", "ibt"}
					if tc.target == "arm64-linux" {
						hardware = []string{"pac", "bti", "mte"}
					}
					for _, name := range hardware {
						row, ok := rows[name]
						if !ok {
							t.Errorf("no %s row:\n%s", name, run)
							continue
						}
						for _, cell := range []string{"compiled=", "supported=", "requested=", "enabled=", "verified="} {
							if !strings.Contains(row, cell) {
								t.Errorf("row %s lacks %s: %q", name, cell, row)
							}
						}
						if name != "mte" && !strings.Contains(row, "compiled="+p.harden) {
							t.Errorf("row %s = %q, want compiled=%s", name, row, p.harden)
						}
					}
					if p.name == "hardened" {
						t.Logf("%s hardened status:\n%s", tc.target, run)
					}
				})
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		if _, errout, code := fern("-profile", "bogus", "-target", "x86-64-linux", "-o", filepath.Join(dir, "x"), src, cli.stdlib); code != 2 || !strings.Contains(errout, "fern: -profile takes safe, hardened, diagnostic or minimal (got \"bogus\")") {
			t.Errorf("a bad profile: exit %d, stderr %q", code, errout)
		}
		if _, errout, code := fern("-profile", "hardened", "-target", "wasm32-wasi", "-o", filepath.Join(dir, "x.wasm"), src, cli.stdlib); code != 2 || !strings.Contains(errout, "-harden is only supported") {
			t.Errorf("a hardened wasm build: exit %d, stderr %q", code, errout)
		}
		if out, errout, code := fern("-manifest", src); code != 1 || !strings.Contains(errout, "no build manifest in") || out != "" {
			t.Errorf("-manifest on a source file: exit %d, stdout %q, stderr %q", code, out, errout)
		}
	})
}
