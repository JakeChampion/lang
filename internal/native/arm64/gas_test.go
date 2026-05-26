package arm64_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// TestAssembleBasic checks Assemble without external tools: a tiny
// exit(42) snippet must produce the known movz/movz/svc bytes, and
// labels + a comment must be handled.
func TestAssembleBasic(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\t.global _start\n" +
		"_start:\n" +
		"\tmov x0, #42   // exit status\n" +
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	got, err := arm64.Assemble(src)
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	want = arm64.Put(want, arm64.MOVZ(0, 42, 0))
	want = arm64.Put(want, arm64.MOVZ(8, 93, 0))
	want = arm64.Put(want, arm64.SVC(0))
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestAssembleErrors(t *testing.T) {
	if _, err := arm64.Assemble("\tfjcvtzs x0, d1\n"); err == nil {
		t.Error("expected error for unsupported instruction")
	}
	if _, err := arm64.Assemble("\t.quad 5\n"); err == nil {
		t.Error("expected error for unsupported directive")
	}
	if _, err := arm64.Assemble("\tb nowhere\n"); err == nil {
		t.Error("expected error for undefined label")
	}
}

// TestAssembleAgainstGNUAs is the byte-exact oracle: each snippet is
// assembled both by Assemble and by aarch64-linux-gnu-as, and the
// .text bytes must match. This pins the whole parser+encoder stack to
// an independent reference.
func TestAssembleAgainstGNUAs(t *testing.T) {
	as, objcopy := findBinutils(t)

	cases := map[string]string{
		"moves": "" +
			"\tmov x0, #42\n\tmov x1, x0\n\tmovz x8, #93\n\tmovk x3, #0xabcd\n",
		"arith": "" +
			"\tadd x0, x1, #1\n\tadd x2, x3, #1, lsl #12\n\tsub x4, x5, x6\n\tadd x0, x1, x2\n",
		"logical_mul_shift": "" +
			"\tand x0, x1, x2\n\torr x0, x1, x2\n\teor x0, x1, x2\n\tmul x0, x1, x2\n\tlsl x0, x1, x2\n\tlsr x0, x1, x2\n\tasr x0, x1, x2\n",
		"compare": "" +
			"\tcmp x1, x2\n\tcmp x1, #5\n",
		"branch_regs": "" +
			"\tbr x0\n\tblr x1\n\tret\n\tsvc #0\n",
		"labels_and_branches": "" +
			"loop:\n\tcmp x0, #0\n\tb.eq done\n\tsub x0, x0, #1\n\tcbnz x0, loop\n\tb loop\ndone:\n\tbeq loop\n\tret\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := arm64.Assemble(src)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			want := gnuAsText(t, as, objcopy, src)
			if !bytes.Equal(got, want) {
				t.Fatalf("bytes differ from aarch64-linux-gnu-as:\n got  % x\n want % x", got, want)
			}
		})
	}
}

func findBinutils(t *testing.T) (as, objcopy string) {
	t.Helper()
	var err error
	if as, err = exec.LookPath("aarch64-linux-gnu-as"); err != nil {
		t.Skip("aarch64-linux-gnu-as not on PATH")
	}
	if objcopy, err = exec.LookPath("aarch64-linux-gnu-objcopy"); err != nil {
		t.Skip("aarch64-linux-gnu-objcopy not on PATH")
	}
	return as, objcopy
}

// gnuAsText assembles src with GNU as and extracts the raw .text bytes.
func gnuAsText(t *testing.T, as, objcopy, src string) []byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "in.s")
	oPath := filepath.Join(dir, "in.o")
	binPath := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, sPath, "-o", oPath).CombinedOutput(); err != nil {
		t.Fatalf("as: %v\n%s", err, out)
	}
	if out, err := exec.Command(objcopy, "-O", "binary", "--only-section=.text", oPath, binPath).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	b, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
