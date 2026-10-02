package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The emitted constant-byte arrays use .balign 8 between adjacent arrays.
// Its argument is a byte count, while ARM64 .align/.p2align use exponents.
const arm64ByteAlignmentProgram = `
function main(): i32 {
    let directives: string[] = [".balign 0", ".balign 1", ".balign 2", ".balign 4", ".balign 8", ".balign 16", ".align 0", ".align 1", ".align 3", ".p2align 4"];
    let amounts: i32[] = [1, 1, 2, 4, 8, 16, 1, 2, 8, 16];
    let i: i32 = 0;
    while (i < directives.len()) {
        let body = ".byte 17\n" + directives[i] + "\nfirst:\n.byte 99\n" + directives[i] + "\nnext:\n.byte 23\n";
        let amount = amounts[i];
        let data = arm64_gas_program(".data\n" + body);
        // The assembler rounds the completed data segment to eight bytes.
        let data_size: i32 = ((amount * 2 + 1 + 7) / 8) * 8;
        if (data.unknown.len() != 0 || data.data.len() != data_size) { return 1; }
        if (arm64_gas_dlabel_off(data, "first") != amount || arm64_gas_dlabel_off(data, "next") != amount * 2) { return 2; }
        let at: i32 = 0;
        while (at < data.data.len()) {
            let expected: i32 = 0;
            if (at == 0) { expected = 17; }
            if (at == amount) { expected = 99; }
            if (at == amount * 2) { expected = 23; }
            if (data.data[at] != expected) { return 3; }
            at = at + 1;
        }
        let bss_body = ".skip 1\n" + directives[i] + "\nfirst:\n.skip 1\n" + directives[i] + "\nnext:\n.skip 1\n";
        let bss = arm64_gas_program(".bss\n" + bss_body);
        if (bss.unknown.len() != 0 || bss.data.len() != 0 || bss.bss_size != amount * 2 + 1) { return 4; }
        if (arm64_gas_bss_off(bss, "first") != amount || arm64_gas_bss_off(bss, "next") != amount * 2) { return 5; }
        i = i + 1;
    }
    let aligned_data = arm64_gas_program(".data\n.quad 0\n.balign 8\nvalue:\n.byte 7\n");
    if (aligned_data.data.len() != 16 || arm64_gas_dlabel_off(aligned_data, "value") != 8) { return 6; }
    let aligned_bss = arm64_gas_program(".bss\n.skip 8\n.balign 8\nvalue:\n.skip 1\n");
    if (aligned_bss.bss_size != 9 || arm64_gas_bss_off(aligned_bss, "value") != 8) { return 7; }
    return 0;
}
`

func TestSelfHostArm64ByteAlignment(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := arm64NativeSrc(t) + arm64ByteAlignmentProgram
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target, "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1"); code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
		})
	}
}

func TestSelfHostArm64DarwinByteAlignment(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	src, bin := filepath.Join(dir, "alignment.fern"), filepath.Join(dir, "alignment")
	if err := os.WriteFile(src, []byte(arm64NativeSrc(t)+arm64ByteAlignmentProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "-target", "arm64-darwin", src, e2eharness.SelfHostStdlibRoot(t), "-o", bin)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
}
