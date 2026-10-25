package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Each program prints what tcp_recv_into, udp_recvfrom, reactor_wait or
// __arr_set_len left in every binding of the array it wrote. The self-host
// interpreter, the Go interpreter and the x86-64 build must print the same.
func TestSelfHostInterpArrayIdentityMatchesOracleAndX86_64(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the programs use Linux sockets and the reactor")
	}
	cli := buildSelfHostCLI(t)
	oracle := buildFernCLIBin(t)
	files, err := filepath.Glob(filepath.Join("testdata", "interp_array_identity", "*.fern"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no programs under testdata/interp_array_identity: %v", err)
	}
	for _, prog := range files {
		prog, _ := filepath.Abs(prog)
		t.Run(strings.TrimSuffix(filepath.Base(prog), ".fern"), func(t *testing.T) {
			sOut, sErr, sCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, "-interp", prog, cli.stdlib), "")
			oOut, oErr, oCode := runInterpCLI(t, exec.Command(oracle, "-interp", prog), "")
			xOut, xErr, xCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.x86Binary(t, prog)), "")
			if sOut != xOut || sCode != xCode || oOut != xOut || oCode != xCode {
				t.Errorf("the three runs disagree\n"+
					"fern -interp:        exit %d\nstdout:\n%s\nstderr:\n%s\n"+
					"fern-oracle -interp: exit %d\nstdout:\n%s\nstderr:\n%s\n"+
					"x86-64:              exit %d\nstdout:\n%s\nstderr:\n%s",
					sCode, sOut, sErr, oCode, oOut, oErr, xCode, xOut, xErr)
			}
		})
	}
}

// __arr_set_len on one binding changes the length every holder of the array
// sees, as the compiled box does. The Go interpreter changes only the variable
// named at the call (#13186), so this case is held to x86-64 alone.
func TestSelfHostInterpArrSetLenSharedMatchesX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "set_len_shared.fern")
	prog := `import "std/i32";

struct Holder { b: u8[] }

function shrink(b: u8[]): void {
  __arr_set_len(b, 2);
}

function main(): i32 {
  let buf: u8[] = [1u8, 2u8, 3u8, 4u8];
  let alias: u8[] = buf;
  let h: Holder = Holder { b: buf };
  __arr_set_len(buf, 3);
  print(f"{buf.len()} {alias.len()} {h.b.len()}");
  shrink(alias);
  print(f"{buf.len()} {alias.len()} {h.b.len()}");
  __arr_set_len(h.b, 4);
  print(f"{buf.len()} {alias.len()} {h.b.len()} {buf[3]}");
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	sOut, sErr, sCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.bin, "-interp", src, cli.stdlib), "")
	xOut, xErr, xCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)), "")
	if want := "3 3 3\n2 2 2\n4 4 4 4\n"; xOut != want || xCode != 0 {
		t.Fatalf("x86-64: exit %d, stdout %q (stderr %q); want %q", xCode, xOut, xErr, want)
	}
	if sOut != xOut || sCode != xCode {
		t.Errorf("fern -interp: exit %d, stdout %q (stderr %q); x86-64 printed %q", sCode, sOut, sErr, xOut)
	}
}
