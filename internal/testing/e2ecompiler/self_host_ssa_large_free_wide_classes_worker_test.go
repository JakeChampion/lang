package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func wideClassPacket(id, mode uint32, low, high int64, minimum, maximum int32) []byte {
	packet := make([]byte, 36)
	copy(packet, "FWC1")
	binary.LittleEndian.PutUint32(packet[4:8], id)
	binary.LittleEndian.PutUint32(packet[8:12], mode)
	binary.LittleEndian.PutUint64(packet[12:20], uint64(low))
	binary.LittleEndian.PutUint64(packet[20:28], uint64(high))
	binary.LittleEndian.PutUint32(packet[28:32], uint32(minimum))
	binary.LittleEndian.PutUint32(packet[32:36], uint32(maximum))
	return packet
}

func TestSelfHostLargeFreeWideClassesWorker(t *testing.T) {
	cli := buildSelfHostCLI(t)
	entry, err := filepath.Abs("../../../compiler/drivers/ssalargefreewideclasses_worker.fern")
	if err != nil {
		t.Fatal(err)
	}
	flags := []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
	valid := wideClassPacket(17, 0, 536870909, 536870909, 0, 0)
	invalid := wideClassPacket(18, 2, 0, 1, 0, 7)
	want := "class 17 52 -2 0 0 0 0\nend 17\n"
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
			if stdout != want+"class 18 -1 -1 0 0 0 0\nend 18\n" {
				t.Fatalf("worker protocol output: %q", stdout)
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
				{"short-header", []byte("FWC")},
				{"truncated-source", valid[:len(valid)-1]},
				{"magic", magic},
				{"id-range", id},
				{"mode-range", wideClassPacket(19, 6, 0, 0, 0, 0)},
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
