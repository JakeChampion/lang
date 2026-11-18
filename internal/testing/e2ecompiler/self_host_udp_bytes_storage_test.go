package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostUDPBytesStorage(t *testing.T) {
	primary, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	// A sanitized build holds each released block in its quarantine, so the
	// bump position is stable only in a build without the sanitizer.
	stable := "if (used != stable) { return -1000; }"
	for _, compiler := range []struct {
		name, cli, emit, stable string
		env                     []string
	}{
		{"primary", primary, "core-module", stable, []string{"FERN_LEAKCHECK=1"}},
		{"primary-sanitized", primary, "core-module", "", []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"}},
		{"bootstrap", bootstrap, "command-module", stable, nil},
	} {
		for _, size := range []int{0, 8193} {
			t.Run(fmt.Sprintf("%s/bytes%d", compiler.name, size), func(t *testing.T) {
				dir := t.TempDir()
				src, bin := filepath.Join(dir, "storage.fern"), filepath.Join(dir, "storage.wasm")
				program := fmt.Sprintf(`function send_once(data: u8[]): i32 {
    let s: Socket = udp_bind([0u8, 0u8, 0u8, 0u8], 0);
    if (s.fd < 0) { return s.fd; }
    let n: i32 = udp_sendto_bytes(s, [127u8, 0u8, 0u8, 1u8], 1, data);
    tcp_close(s);
    return n;
}
function main(): i32 {
    let data: u8[] = [];
    for i in 0..%d { data = data.append((i %% 256) as u8); }
    let stable: i64 = 0;
    let result: i32 = 0;
    for i in 0..32 {
        result = send_once(data);
        if (result >= 0) {
            if (result != data.len()) { return -1002; }
            result = 1;
        }
        let used: i64 = __heap_bump_bytes();
        if (i == 0) { stable = used; }
        %s
        if (data.len() != %d) { return -1003; }
        for j in 0..data.len() { if (data[j] != (j %% 256) as u8) { return -1004; } }
    }
    return result;
}`, size, compiler.stable, size)
				if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-target", "wasm32-wasi", "-emit", compiler.emit, "-o", bin, src}
				if compiler.cli == primary {
					args = append(args, stdlib)
				}
				cmd := exec.Command(compiler.cli, args...)
				cmd.Env = append(os.Environ(), compiler.env...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				e2eharness.CheckWasiSocketReclaim(t, bin, "udp")
			})
		}
	}
}
