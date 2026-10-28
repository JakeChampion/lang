package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func wideDecoderPacket(id uint32, source []byte) []byte {
	packet := make([]byte, 12+len(source))
	copy(packet, "FWI1")
	binary.LittleEndian.PutUint32(packet[4:8], id)
	binary.LittleEndian.PutUint32(packet[8:12], uint32(len(source)))
	copy(packet[12:], source)
	return packet
}

func TestSelfHostLargePushWideDecoderWorker(t *testing.T) {
	cli := buildSelfHostCLI(t)
	entry, err := filepath.Abs("../../../compiler/drivers/ssalargepushwideisa_worker.fern")
	if err != nil {
		t.Fatal(err)
	}
	flags := []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
	valid := wideDecoderPacket(17, []byte(".text\n__fern_large_push:\nmovabs $-9223372036854775808, %r15\nret\n__fern_capw:\nret\n"))
	invalid := wideDecoderPacket(18, []byte(".text\n__fern_large_push:\nunknown_instruction\nret\n__fern_capw:\nret\n"))
	want := "wid 17 ok 2\nop 17 0 41 64 -1 15 -1 -1 1 0 0 -9223372036854775808 - -1 -\nop 17 1 18 0 -1 -1 -1 -1 1 0 0 0 - -1 -\nend 17\n"
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var command func() *exec.Cmd
			switch target {
			case "x86-64-linux":
				bin := cli.x86Binary(t, entry, flags...)
				command = func() *exec.Cmd { return runX86_64Bin(cli.runner, bin) }
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, entry, target, flags...))
				if err != nil {
					t.Fatal(err)
				}
				bin := buildBinArm64(t, gcc, t.TempDir(), "worker", string(asm))
				command = func() *exec.Cmd { return runArm64Bin(qemu, bin) }
			case "wasm32-wasi":
				wat := cli.emit(t, entry, target, flags...)
				command = func() *exec.Cmd { return exec.Command(e2eharness.Wasmtime(t), "run", wat) }
			}
			run := func(t *testing.T, input []byte) (string, string, int) {
				cmd := command()
				cmd.Env = append(os.Environ(), flags...)
				cmd.Stdin = bytes.NewReader(input)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				_ = cmd.Run()
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
					t.Fatalf("worker did not exit normally: %s", stderr.String())
				}
				return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
			}
			stdout, stderr, code := run(t, append(append([]byte{}, valid...), invalid...))
			if code != 0 {
				t.Fatalf("worker exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
			lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
			if len(lines) != 6 || strings.Join(lines[:4], "\n")+"\n" != want || !strings.HasPrefix(lines[4], "wid 18 error 0 ") || lines[5] != "end 18" {
				t.Fatalf("worker protocol output: %q", stdout)
			}
			message, err := hex.DecodeString(strings.TrimPrefix(lines[4], "wid 18 error 0 "))
			if err != nil || len(message) == 0 || !utf8.Valid(message) {
				t.Fatalf("worker error encoding: %q", lines[4])
			}
			magic := append([]byte{}, valid...)
			magic[0] = 'X'
			id := append([]byte{}, valid...)
			binary.LittleEndian.PutUint32(id[4:8], ^uint32(0))
			for _, fault := range []struct {
				name  string
				input []byte
			}{
				{"empty", nil},
				{"short-header", []byte("FWI")},
				{"truncated-source", valid[:len(valid)-1]},
				{"magic", magic},
				{"id-range", id},
				{"invalid-utf8", wideDecoderPacket(19, []byte{0xff})},
				{"trailing-data", append(append([]byte{}, valid...), '?')},
			} {
				t.Run(fault.name, func(t *testing.T) {
					out, census, exit := run(t, fault.input)
					if exit != 2 {
						t.Fatalf("packet fault exit %d: %s\n%s", exit, out, census)
					}
					assertBalancedCensus(t, census)
					if fault.name == "trailing-data" && out != want {
						t.Fatalf("completed prefix changed: %q", out)
					}
				})
			}
		})
	}
}
