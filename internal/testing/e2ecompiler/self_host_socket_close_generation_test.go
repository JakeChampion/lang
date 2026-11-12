package e2ecompiler

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A task parks on a connection. Its parent closes the connection and opens a
// second one, which gets the descriptor number of the first. The peer writes
// on the second. The parked read must report the close, not those bytes.
func TestSelfHostParkedReadSeesAClose(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			defer close(done)
			go func() {
				first, err := listener.Accept()
				if err != nil {
					return
				}
				defer first.Close()
				second, err := listener.Accept()
				if err != nil {
					return
				}
				defer second.Close()
				time.Sleep(200 * time.Millisecond)
				_, _ = second.Write([]byte("SECRET"))
				<-done
			}()
			dir := t.TempDir()
			src := filepath.Join(dir, "parked.fern")
			program := fmt.Sprintf(`import "std/net";
import "std/async";
import "std/time";

function show(r: Result[net.Recv, net.NetError]): string {
  match (r) {
    Ok(net.Chunk(b)) => { return "chunk " + b.len().to_string(); },
    Ok(_) => { return "no data"; },
    Err(Other(n)) => { return "error " + n.to_string(); },
    Err(_) => { return "error"; }
  }
}

function run(own c1: Socket): string {
  let fd1 = c1.fd;
  let f = async.spawn((): string => show(net.read_deadline(c1, 16, time.duration_millis(2000i64))));
  let closed = tcp_close(c1);
  let c2: Socket = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %[1]d, false);
  print("same descriptor: " + (c2.fd == fd1).to_string());
  match (async.block_on(f)) {
    Some(v) => { return v; },
    None => { return "cancelled"; }
  }
}

function main(): i32 {
  let c1: Socket = tcp_connect_with([127u8, 0u8, 0u8, 1u8], %[1]d, false);
  print(run(c1));
  return 0;
}
`, listener.Addr().(*net.TCPAddr).Port)
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			var bin string
			var runner []string
			switch target {
			case "x86-64-linux":
				bin = cli.x86Binary(t, src)
				runner = cli.runner
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target))
				if err != nil {
					t.Fatal(err)
				}
				bin = buildBinArm64(t, gcc, t.TempDir(), "parked", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				// Sockets need the component, which the shipped CLI links.
				primary, stdlib := witSelfHostCLI(t)
				bin = filepath.Join(dir, "parked.wasm")
				if out, err := exec.Command(primary, "-target", target, "-o", bin, src, stdlib).CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				runner = []string{"wasmtime", "run", "-S", "inherit-network"}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			args := append(append([]string{}, runner...), bin)
			out, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) != 2 {
				t.Fatalf("want 2 lines, got %q", out)
			}
			// The test proves nothing unless the number is reused.
			if lines[0] != "same descriptor: true" {
				t.Fatalf("the second connection did not reuse the descriptor: %q", out)
			}
			if lines[1] != "error 9" {
				t.Fatalf("the parked read gave %q, want the closed error %q", lines[1], "error 9")
			}
		})
	}
}
