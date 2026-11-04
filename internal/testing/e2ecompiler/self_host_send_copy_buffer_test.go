package e2ecompiler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

const sendCopyBufferProg = `struct Packet { data: u8[] }

function transfer(client: Socket, server: Socket, data: u8[], value: u8): boolean {
  return tcp_send_bytes(client, [value]) == 1 && tcp_recv_into(server, data) == 1;
}

function main(): i32 {
  let lo: u8[] = [127u8, 0u8, 0u8, 1u8];
  let listener: Socket = tcp_listen_with(lo, 0, 16, false);
  if (listener.fd < 0) { return 2; }
  let client: Socket = tcp_connect_with(lo, tcp_local_port(listener), false);
  let server: Socket = tcp_accept(listener);
  let original: Packet = Packet { data: [0u8] };
  if (%t && !transfer(client, server, original.data, 65u8)) { return 3; }
  let copied: Packet = __send_copy(original);
  if (copied.data[0] != original.data[0]) { return 4; }
  if (!transfer(client, server, copied.data, 68u8)) { return 5; }
  let closed: i32 = tcp_close(server) + tcp_close(client) + tcp_close(listener);
  if (closed != 0) { return 6; }
  print(original.data[0].to_string() + " " + copied.data[0].to_string());
  return 0;
}
`

func TestSelfHostSendCopySeparatesBufferStores(t *testing.T) {
	h := selfHostCLIForHost(t)
	leak := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`)
	for _, before := range []bool{false, true} {
		t.Run(fmt.Sprintf("write-before-copy=%t", before), func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "copy.fern")
			if err := os.WriteFile(src, []byte(fmt.Sprintf(sendCopyBufferProg, before)), 0o644); err != nil {
				t.Fatal(err)
			}
			want := "0 68\n"
			if before {
				want = "65 68\n"
			}
			run := func(t *testing.T, executable string, args []string, sanitized bool) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, executable, args...)
				cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("run: %v; stdout %q; stderr %s", err, &stdout, &stderr)
				}
				if stdout.String() != want {
					t.Fatalf("stdout %q, want %q", &stdout, want)
				}
				if sanitized {
					if m := leak.FindStringSubmatch(stderr.String()); len(m) != 3 || m[1] != m[2] {
						t.Fatalf("unbalanced sanitized run: %s", &stderr)
					}
				}
			}
			for _, engine := range []struct{ name, cli string }{{"go-interp", h.native}, {"primary-interp", h.cli}} {
				t.Run(engine.name, func(t *testing.T) {
					run(t, engine.cli, []string{"-interp", src, h.stdlib}, false)
				})
			}
			for _, target := range h.targets {
				t.Run(target.target, func(t *testing.T) {
					bin := filepath.Join(dir, target.target)
					h.compileWith(t, target, src, bin, "-sanitize")
					if len(target.runner) == 0 {
						run(t, bin, nil, true)
					} else {
						run(t, target.runner[0], append(target.runner[1:], bin), true)
					}
				})
			}
		})
	}
}
