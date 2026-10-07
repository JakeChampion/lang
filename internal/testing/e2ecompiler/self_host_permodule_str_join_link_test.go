package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostPerModuleStrJoinLinks: a LIBRARY unit that joins a string array
// links per module and answers. The entry unit defines __fn___fern_arr_str_join
// once and must export it; it did not until std/string joined the compiler's
// own module set and the emit-all fixpoint stopped linking on reverse_words.
func TestSelfHostPerModuleStrJoinLinks(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "strjoinlinkdriver")

	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", `@noinline pub function tag(xs: string[]): string {
  return xs.join("-");
}
`)
	mustWrite(t, proj, "main.fern", `import "./leaf";

function main(): i32 { return leaf.tag(["a", "b"]).len() + 38; }
`)
	copyStdlibTree(t, proj)
	entry := filepath.Join(proj, "main.fern")

	for _, tc := range []struct{ target, gcc string }{{"x86-64-linux", x86gcc}, {"arm64-linux", ""}} {
		t.Run(tc.target, func(t *testing.T) {
			gcc := tc.gcc
			var qemu string
			if tc.target == "arm64-linux" {
				gcc, qemu = arm64Tooling(t)
			}
			drive := func(args ...string) string {
				t.Helper()
				out, err := runX86_64Bin(x86runner, driverBin, append([]string{entry, "-target", tc.target}, args...)...).Output()
				if err != nil {
					t.Fatalf("driver %v: %v", args, err)
				}
				return string(out)
			}
			n, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
			if err != nil || n < 2 {
				t.Fatalf("-per-module-count gave n=%d (%v), want >=2", n, err)
			}
			var needArgs []string
			for _, ln := range strings.Split(drive("-per-module-needs"), "\n") {
				if s := strings.TrimSpace(ln); s != "" {
					needArgs = append(needArgs, "-extra-need", s)
				}
			}
			var objs []string
			for i := 0; i < n; i++ {
				unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
				objs = append(objs, mustWrite(t, proj, tc.target+"_u"+strconv.Itoa(i)+".s", unit))
			}
			bin := filepath.Join(proj, tc.target+"_prog")
			linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
			if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
				t.Fatalf("per-module link failed — a library unit calls a helper with no exported definer: %v\n%s", err, lout)
			}
			var cmd *exec.Cmd
			if tc.target == "arm64-linux" {
				cmd = runArm64Bin(qemu, bin)
			} else {
				cmd = runX86_64Bin(x86runner, bin)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 41 {
				t.Errorf("per-module %s join program exited %d, want 41", tc.target, code)
			}
			_ = os.Remove(bin)
		})
	}
}
