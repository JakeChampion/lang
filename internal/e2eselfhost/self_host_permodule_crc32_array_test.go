package e2eselfhost

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The data entry must be exported when a library calls it and the entry
// module supplies the runtime. A single-module link cannot detect that gap.
func TestSelfHostPerModuleArm64CRC32ArrayLinks(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	x86gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "crcdriver")
	proj := t.TempDir()
	mustWrite(t, proj, "leaf.fern", `pub function checksum(): i32 {
    let bytes: u8[] = [0 as u8, 255 as u8, 128 as u8];
    return __crc32_cksum_array(123, bytes);
}`)
	var crc uint32 = 123
	for _, b := range []byte{0, 255, 128} {
		crc ^= uint32(b) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	mustWrite(t, proj, "main.fern", "import \"./leaf\";\nfunction main(): i32 { if (leaf.checksum() != ("+strconv.FormatInt(int64(int32(crc)), 10)+"i64 as i32)) { return 1; } return 0; }")
	copyStdlibTree(t, proj)
	entry := filepath.Join(proj, "main.fern")
	drive := func(args ...string) string {
		t.Helper()
		cmd := runX86_64Bin(runner, driver, append([]string{entry, "-target", "arm64-linux"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("driver %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(drive("-per-module-count")))
	if err != nil || n < 2 {
		t.Fatalf("module count=%d, error=%v", n, err)
	}
	var needs, objects []string
	for _, need := range strings.Fields(drive("-per-module-needs")) {
		needs = append(needs, "-extra-need", need)
	}
	sawCall := false
	for i := 0; i < n; i++ {
		unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needs...)...)
		sawCall = sawCall || strings.Contains(unit, "bl __fern_crc32_cksum_data")
		objects = append(objects, mustWrite(t, proj, "u"+strconv.Itoa(i)+".s", unit))
	}
	if !sawCall {
		t.Fatal("fixture did not call the CRC data entry")
	}
	bin := filepath.Join(proj, "crc")
	args := append([]string{"-static", "-nostdlib", "-no-pie"}, objects...)
	args = append(args, "-o", bin)
	if out, err := exec.Command(armgcc, args...).CombinedOutput(); err != nil {
		t.Fatalf("per-module link: %v\n%s", err, out)
	}
	if out, err := runArm64Bin(qemu, bin).CombinedOutput(); err != nil {
		t.Fatalf("per-module result: %v\n%s", err, out)
	}
}
