package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// fern-oracle -interp gives an array the compiled box: __arr_set_len reaches
// every holder of it, and an append that kept the source alive hands back a
// copy that a receive does not write through.
func TestOracleInterpArrayBoxMatchesX86_64(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the program uses Linux sockets")
	}
	cli := buildSelfHostCLI(t)
	oracle := buildFernCLIBin(t)
	src := filepath.Join(t.TempDir(), "array_box.fern")
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
  print(f"alias and field: {buf.len()} {alias.len()} {h.b.len()}");
  shrink(alias);
  print(f"parameter: {buf.len()} {alias.len()} {h.b.len()}");
  __arr_set_len(h.b, 4);
  print(f"through the field: {buf.len()} {alias.len()} {h.b.len()} {buf[3]}");
  let view: [u8] = buf[0:4];
  __arr_set_len(buf, 1);
  print(f"view: {view.len()} {view[3]} buf {buf.len()}");

  let lo: u8[] = [127u8, 0u8, 0u8, 1u8];
  let ls: i32 = tcp_listen_with(lo, 0, 16, false);
  let c: i32 = tcp_connect_with(lo, tcp_local_port(ls), false);
  let s: i32 = tcp_accept(ls);
  let a: u8[] = [1u8];
  let b: u8[] = a.append(2u8);
  let d: u8[] = b.append(3u8);
  let sent: i32 = tcp_send_bytes(c, "A".bytes());
  let n: i32 = tcp_recv_into(s, d);
  print(f"append then receive: {sent} {n} {a[0]} {b[0]} {d[0]}");
  let e: u8[] = b.append(4u8);
  sent = tcp_send_bytes(c, "B".bytes());
  n = tcp_recv_into(s, b);
  print(f"receive into the source: {sent} {n} {b[0]} {d[0]} {e[0]} {e[2]}");
  __arr_set_len(b, 3);
  print(f"regrow past a shared append: {b[2]} {e[2]}");
  print(f"close: {tcp_close(c)} {tcp_close(s)} {tcp_close(ls)}");
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "alias and field: 3 3 3\n" +
		"parameter: 2 2 2\n" +
		"through the field: 4 4 4 4\n" +
		"view: 4 4 buf 1\n" +
		"append then receive: 1 1 1 1 65\n" +
		"receive into the source: 1 1 66 65 1 4\n" +
		"regrow past a shared append: 0 4\n" +
		"close: 0 0 0\n"
	xOut, xErr, xCode := runInterpCLI(t, runX86_64Bin(cli.runner, cli.x86Binary(t, src)), "")
	if xOut != want || xCode != 0 {
		t.Fatalf("x86-64: exit %d, stdout %q (stderr %q); want %q", xCode, xOut, xErr, want)
	}
	oOut, oErr, oCode := runInterpCLI(t, exec.Command(oracle, "-interp", src), "")
	if oOut != xOut || oCode != xCode {
		t.Errorf("fern-oracle -interp: exit %d, stdout %q (stderr %q); x86-64 printed %q", oCode, oOut, oErr, xOut)
	}
}
