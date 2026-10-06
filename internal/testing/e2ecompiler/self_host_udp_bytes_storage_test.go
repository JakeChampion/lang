package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Every setup/send failure must close owned resources and reclaim guest
// scratch. Repeating the same operation also detects packing-buffer leaks.
func TestSelfHostUDPBytesStorage(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	for _, compiler := range []struct{ name, cli, emit string }{
		{"primary", primary, "core-module"}, {"bootstrap", bootstrap, "command-module"},
	} {
		for _, size := range []int{0, 8193} {
			t.Run(fmt.Sprintf("%s/bytes%d", compiler.name, size), func(t *testing.T) {
				dir := t.TempDir()
				src, bin := filepath.Join(dir, "storage.fern"), filepath.Join(dir, "storage.wasm")
				program := fmt.Sprintf(`function main(): i32 {
    let data: u8[] = [];
    for i in 0..%d { data = data.append((i %% 256) as u8); }
    let stable: i64 = 0;
    let result: i32 = 0;
    for i in 0..32 {
        result = udp_send_bytes("127.0.0.1", 1, data);
        if (result >= 0) {
            if (result != data.len()) { return -1002; }
            result = 1;
        }
        let used: i64 = __heap_bump_bytes();
        if (i == 0) { stable = used; }
        if (used != stable) { return -1000; }
        if (data.len() != %d) { return -1003; }
        for j in 0..data.len() { if (data[j] != (j %% 256) as u8) { return -1004; } }
    }
    return result;
}`, size, size)
				if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-target", "wasm32-wasi", "-emit", compiler.emit, "-o", bin, src}
				if compiler.name == "primary" {
					args = append(args, stdlib)
				}
				cmd := exec.Command(compiler.cli, args...)
				if compiler.name == "primary" {
					cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1", "FERN_SANITIZE=1")
				}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				e2eharness.CheckWasiSocketReclaim(t, bin, "udp")
			})
		}
	}
}
