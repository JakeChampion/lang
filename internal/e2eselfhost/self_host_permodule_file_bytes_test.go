package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A library-only caller must link against the entry unit's shared runtime.
// Whole-program compilation cannot detect an omitted per-module runtime root.
func TestSelfHostPerModuleFileBytes(t *testing.T) {
	checkSelfHostPerModuleByteSink(t, `pub function save(): i32 {
  let data: u8[] = [255 as u8, 0 as u8, 128 as u8, 65 as u8];
  match (write_file_bytes("raw.bin", data)) {
    Ok(_) => { return 0; },
    Err(_) => { return 1; }
  }
}
`, func(t *testing.T, dir string) {
		got, err := os.ReadFile(filepath.Join(dir, "raw.bin"))
		want := []byte{255, 0, 128, 65}
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("file bytes = %v (%v), want %v", got, err, want)
		}
	})
}

func TestSelfHostPerModuleTCPSendBytes(t *testing.T) {
	checkSelfHostPerModuleByteSink(t, `pub function save(): i32 {
  let data: u8[] = [255 as u8, 0 as u8, 128 as u8];
  if (tcp_send_bytes(-1, data) != -9) { return 1; }
  if (data.len() != 3 || data[0] != 255 || data[1] != 0 || data[2] != 128) { return 2; }
  return 0;
}
`, nil)
}

func TestSelfHostPerModuleUDPSendBytes(t *testing.T) {
	checkSelfHostPerModuleByteSink(t, `pub function save(): i32 {
  let data: u8[] = [255u8, 0u8, 128u8];
  if (udp_send_bytes("invalid", 1, data) >= 0) { return 1; }
  if (udp_sendto_bytes(-1, [127u8, 0u8, 0u8, 1u8], 1, data) != -9) { return 2; }
  if (data.len() != 3 || data[0] != 255 || data[1] != 0 || data[2] != 128) { return 3; }
  return 0;
}
`, nil)
}

func checkSelfHostPerModuleByteSink(t *testing.T, leaf string, check func(*testing.T, string)) {
	t.Helper()
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "bytesinkdriver")
	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", leaf)
	mustWrite(t, proj, "main.fern", `import "./leaf";
function main(): i32 { return leaf.save(); }
`)
	copyStdlibTree(t, proj)
	entry := filepath.Join(proj, "main.fern")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			gcc := x86gcc
			var qemu string
			if target == "arm64-linux" {
				gcc, qemu = arm64Tooling(t)
			}
			drive := func(args ...string) string {
				t.Helper()
				out, err := runX86_64Bin(x86runner, driver, append([]string{entry, "-target", target}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("driver %v: %v\n%s", args, err, out)
				}
				return string(out)
			}
			n, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
			if err != nil || n < 2 {
				t.Fatalf("per-module count = %d (%v), want >=2", n, err)
			}
			var needArgs []string
			for _, root := range strings.Fields(drive("-per-module-needs")) {
				needArgs = append(needArgs, "-extra-need", root)
			}
			var objects []string
			for i := 0; i < n; i++ {
				unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
				objects = append(objects, mustWrite(t, proj, target+"_u"+strconv.Itoa(i)+".s", unit))
			}
			bin := filepath.Join(proj, target+"_prog")
			linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, objects...)
			linkArgs = append(linkArgs, "-o", bin)
			if out, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
				t.Fatalf("per-module link: %v\n%s", err, out)
			}
			var run *exec.Cmd
			if target == "arm64-linux" {
				run = runArm64Bin(qemu, bin)
			} else {
				run = runX86_64Bin(x86runner, bin)
			}
			run.Dir = t.TempDir()
			if out, err := run.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			if check != nil {
				check(t, run.Dir)
			}
		})
	}
}
