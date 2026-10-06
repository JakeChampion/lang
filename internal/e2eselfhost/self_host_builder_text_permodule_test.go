package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A library module owns the builder calls; the entry must supply and export
// text extraction even when it has no direct builder operation itself.
func TestSelfHostBuilderTextPerModule(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "builder-modules")
	project := t.TempDir()
	entry := filepath.Join(project, "main.fern")
	for name, source := range map[string]string{
		"main.fern": "import \"./builders\"; function main(): i32 { return builders.run(); }",
		"builders.fern": `pub function run(): i32 {
  let h: usize = buf_new(1);
  buf_push_byte(h, 226); buf_push_byte(h, 130);
  let text: string = buf_take(h);
  if (buf_len(h) != 0) { buf_free(h); return 1; }
  buf_push(h, "again");
  let next: string = buf_take(h);
  buf_free(h);
  if (text != "�" || next != "again") { return 2; }
  return 0;
}`,
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			linker, qemu := gcc, ""
			if target == "arm64-linux" {
				linker, qemu = arm64Tooling(t)
			}
			drive := func(args ...string) []byte {
				t.Helper()
				cmd := runX86_64Bin(runner, driver, append([]string{entry, "-target", target}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("driver %v: %v\n%s", args, err, out)
				}
				return out
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(drive("-per-module-count"))))
			if err != nil || n != 2 {
				t.Fatalf("module count = %d: %v", n, err)
			}
			var needs []string
			for _, line := range strings.Split(string(drive("-per-module-needs")), "\n") {
				if root := strings.TrimSpace(line); root != "" {
					needs = append(needs, "-extra-need", root)
				}
			}
			var units []string
			var asm strings.Builder
			for i := range n {
				unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needs...)...)
				asm.Write(unit)
				path := filepath.Join(t.TempDir(), "unit.s")
				if err := os.WriteFile(path, unit, 0o644); err != nil {
					t.Fatal(err)
				}
				units = append(units, path)
			}
			if !strings.Contains(asm.String(), ".globl __fern_buf_take\n") {
				t.Fatal("missing text extraction runtime export")
			}
			bin := filepath.Join(t.TempDir(), "builders")
			args := append([]string{"-static", "-nostdlib", "-no-pie", "-o", bin}, units...)
			if out, err := exec.Command(linker, args...).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, out)
			}
			cmd := runX86_64Bin(runner, bin)
			if target == "arm64-linux" {
				cmd = runArm64Bin(qemu, bin)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}
