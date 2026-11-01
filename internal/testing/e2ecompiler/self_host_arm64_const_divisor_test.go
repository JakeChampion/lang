package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostConstDivisorArm64(t *testing.T) {
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not on PATH")
	}
	h := selfHostCLIForHost(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()

	compile := func(t *testing.T, name, src string, args ...string) string {
		t.Helper()
		srcPath := filepath.Join(dir, name+".fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		outPath := filepath.Join(dir, name+".out")
		cmd := exec.Command(h.cli, append(append([]string{"-target", "arm64-linux"}, args...), "-o", outPath, srcPath, h.stdlib)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile %s: %v\n%s", name, err, out)
		}
		return outPath
	}

	t.Run("shapes", func(t *testing.T) {
		const src = `@noinline function d7(x: i32): i32 { return x / 7; }
@noinline function dn7(x: i32): i32 { return x / -7; }
@noinline function m4093(x: i32): i32 { return x % 4093; }
@noinline function d7w(x: i64): i64 { return x / 7i64; }
function main(): i32 { return d7(50) + dn7(50) + m4093(9000) + (d7w(50i64) as i32); }
`
		asmPath := compile(t, "shapes", src, "-emit", "asm")
		asm, err := os.ReadFile(asmPath)
		if err != nil {
			t.Fatalf("read asm: %v", err)
		}
		cases := []struct {
			fn          string
			want, avoid []string
		}{
			{"d7", []string{"=2454267027", "mul x5, x4, x5", "asr x5, x5, #34", "add x5, x5, x5, lsr #63"}, []string{"smulh", "sdiv", "add x5, x5, x4"}},
			{"dn7", []string{"=-2454267027", "mul x5, x4, x5", "asr x5, x5, #34", "add x5, x5, x5, lsr #63"}, []string{"smulh", "sdiv", "sub x5, x5, x4"}},
			{"m4093", []string{"=2149057665", "asr x5, x5, #43", "msub x4, x5, x6, x4"}, []string{"smulh", "sdiv"}},
			{"d7w", []string{"smulh x5, x4, x5", "asr x5, x5, #1"}, []string{"sdiv", "mul x5, x4, x5"}},
		}
		for _, c := range cases {
			body := selfHostFnBody(t, asm, c.fn)
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("%s: expected %q:\n%s", c.fn, w, body)
				}
			}
			for _, a := range c.avoid {
				if strings.Contains(body, a) {
					t.Errorf("%s: %q should be gone:\n%s", c.fn, a, body)
				}
			}
		}
	})

	for _, tc := range constDivisorOracleCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			want := interpExit(t, interpBin, src)
			bin := compile(t, "run_"+tc.name, src)
			if err := os.Chmod(bin, 0o755); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			cmd := exec.Command("qemu-aarch64", bin)
			out, _ := cmd.CombinedOutput()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally (out=%q)", tc.name, out)
			}
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
