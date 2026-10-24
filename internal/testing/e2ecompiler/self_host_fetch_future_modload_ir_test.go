package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostFetchFutureModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	prog := `import "std/async";
import "std/fetch";
import "std/net";
function main(): i32 {
    let none: u8[] = [];
    let f: async.Future[u8[]] = fetch.fetch_future(net.socket_addr(net.ipv4_loopback(), 8080), "/");
    let fs: async.Future[u8[]][] = [f];
    let bodies: u8[][] = async.gather(fs, none);
    return bodies[0].len();
}
`
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	decide, err := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").CombinedOutput()
	if err != nil {
		t.Fatalf("decide: %v\n%s", err, decide)
	}
	if got := strings.TrimSpace(string(decide)); got != "ir" {
		t.Fatalf("fetch_future routed %q, want \"ir\" (pointer-capture closure bailed)", got)
	}
	asm, err := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("loader compile: %v\n%s", err, asm)
	}
	if len(asm) == 0 {
		t.Fatal("loader emitted 0 bytes")
	}
}
