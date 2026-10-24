package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostSocketRuntimePerModule(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_modload_run.fern", "socket-modules")
	project := t.TempDir()
	entry := filepath.Join(project, "main.fern")
	for name, source := range map[string]string{
		"main.fern": "import \"./sockets\"; function main(): i32 { return sockets.run(); }",
		"sockets.fern": `@noinline pub function run(): i32 {
  let lo: u8[] = [127u8, 0u8, 0u8, 1u8];
  let ln: i32 = tcp_listen_with(lo, 0, 4, false);
  if (ln < 0) { return 1; }
  let port: i32 = tcp_local_port(ln);
  let client: i32 = tcp_connect_with(lo, port, false);
  if (client < 0) { tcp_close(ln); return 2; }
  let accepted: i32 = tcp_accept(ln);
  if (accepted < 0) { tcp_close(client); tcp_close(ln); return 3; }
  if (tcp_socket_ctl(client, 1, 1) != 0) { return 4; }
  if (tcp_send_bytes(accepted, [104u8, 105u8]) != 2) { return 5; }
  let got: u8[] = [0u8, 0u8];
  if (tcp_recv_into(client, got) != 2 || got[0] != 104u8 || got[1] != 105u8) { return 6; }
  if (tcp_send_bytes(accepted, [33u8]) != 1) { return 7; }
  let into: u8[] = [0u8];
  if (tcp_recv_into(client, into) != 1 || into[0] != 33u8) { return 8; }
  tcp_close(accepted); tcp_close(client); tcp_close(ln);
  let a: i32 = udp_bind(lo, 0);
  let b: i32 = udp_bind_with(lo, 0, false);
  if (a < 0 || b < 0) { return 9; }
  let bp: i32 = tcp_local_port(b);
  if (udp_sendto(a, lo, bp, "udp") != 3) { return 10; }
  let from: u8[] = [0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8, 0u8];
  let data: u8[] = [0u8, 0u8, 0u8];
  if (udp_recvfrom(b, data, from) != 3 || data[0] != 117u8 || data[2] != 112u8) { return 11; }
  if (udp_connect(a, lo, bp) != 0) { return 12; }
  tcp_close(a); tcp_close(b);
  return 0;
}`,
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			linker, qemu := gcc, ""
			if target == "arm64-linux" {
				linker, qemu = arm64Tooling(t)
			}
			drive := func(args ...string) []byte {
				t.Helper()
				cmd := runX86_64Bin(runner, driver, append([]string{entry, "-target", target}, args...)...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("driver %v: %v\n%s", args, err, out)
				}
				return out
			}
			n, err := strconv.Atoi(strings.TrimSpace(string(drive("-per-module-count"))))
			if err != nil || n != 2 {
				t.Fatalf("module count = %d: %v", n, err)
			}
			var needs []string
			for _, line := range strings.Split(string(drive("-per-module-needs")), "\n") {
				if root := strings.TrimSpace(line); root != "" {
					needs = append(needs, "-extra-need", root)
				}
			}
			var units []string
			var asm strings.Builder
			for i := range n {
				unit := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needs...)...)
				asm.Write(unit)
				path := filepath.Join(t.TempDir(), "unit.s")
				if err := os.WriteFile(path, unit, 0o644); err != nil {
					t.Fatal(err)
				}
				units = append(units, path)
				if !strings.Contains(string(unit), "_start:") && !strings.Contains(string(unit), "__fn_sockets__run:") {
					t.Fatal("library body was inlined away; the test must exercise a cross-unit runtime call")
				}
			}
			for _, helper := range []string{"tcp_listen_with_u8", "tcp_socket_ctl", "tcp_connect_with_u8", "tcp_recv_into_u8", "udp_bind_u8", "udp_bind_with_u8", "udp_sendto_u8", "udp_recvfrom_u8", "udp_connect_u8"} {
				if !strings.Contains(asm.String(), ".globl __fn___fern_"+helper+"\n") {
					t.Errorf("missing socket runtime export: %s", helper)
				}
			}
			bin := filepath.Join(t.TempDir(), "sockets")
			args := append([]string{"-static", "-nostdlib", "-no-pie", "-o", bin}, units...)
			if out, err := exec.Command(linker, args...).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, out)
			}
			cmd := runX86_64Bin(runner, bin)
			if target == "arm64-linux" {
				cmd = runArm64Bin(qemu, bin)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}
