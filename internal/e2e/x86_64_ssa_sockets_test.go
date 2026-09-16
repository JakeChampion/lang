package e2e

import "testing"

// The socket family (tcp_listen, tcp_connect, tcp_accept, tcp_send, tcp_recv,
// tcp_close, tcp_pollable, poll and the wasm pollable stand-ins) and the last
// singletons (isatty, hostname, putchar, create_dir_all, the rc-underflow
// probe) on `-backend ssa -target x86-64-linux`, against the DEFAULT x86-64
// backend on stdout, stderr and exit status.
var x86SSASocketCases = []struct {
	name string
	src  string
}{
	{
		// One process plays both ends over loopback: a connect to a listening
		// socket completes in the kernel's backlog before accept runs.
		name: "loopback_listen_connect_send_poll_recv_close",
		src: `import "std/i32";
function main(): i32 {
  var port: i32 = 48619;
  var l: i32 = tcp_listen(port);
  if (l < 0) { stdout().write("listen-failed\n"); return 1; }
  var again: i32 = tcp_listen(port);
  stdout().write("second-listen=" + again.to_string() + "\n");
  var loopback: i32 = 127 + (1 << 24);
  var c: i32 = tcp_connect(loopback, port);
  if (c < 0) { stdout().write("connect-failed\n"); return 2; }
  var s: i32 = tcp_accept(l);
  if (s < 0) { stdout().write("accept-failed\n"); return 3; }
  var sent: i32 = tcp_send(c, "ping!");
  var fds: i32[] = [tcp_pollable(s)];
  var ready: i32 = poll(fds, 2000);
  var got: u8[] = tcp_recv(s, 16);
  var text: string = string_from_bytes_unchecked(got);
  stdout().write("sent=" + sent.to_string() + " ready=" + ready.to_string() + " got=" + got.len().to_string() + ":" + text + "\n");
  var none: u8[] = tcp_recv(s, 0);
  var idle: i32[] = [c];
  var quiet: i32 = poll(idle, 50);
  stdout().write("empty=" + none.len().to_string() + " quiet=" + quiet.to_string() + "\n");
  stdout().write("timer=" + wasm_timer_pollable(1000).to_string() + " drop=" + wasm_pollable_drop(7).to_string() + "\n");
  var closed: i32 = tcp_close(c) + tcp_close(s) + tcp_close(l);
  var refused: i32 = tcp_connect(loopback, port);
  stdout().write("closed=" + closed.to_string() + " refused=" + refused.to_string() + "\n");
  return 0;
}`,
	},
	{
		// Under the harness stdout is a pipe, so every isatty answer is false;
		// the answers still have to agree between the backends.
		name: "isatty_hostname_and_putchar",
		src: `import "std/i32";
function main(): i32 {
  stdout().write("isatty=" + isatty(1).to_string() + " out=" + stdout().isatty().to_string() + " in=" + stdin().isatty().to_string() + " bad=" + isatty(0 - 1).to_string() + "\n");
  var h: string = hostname();
  if (h.len() > 0) { stdout().write("hostname-set\n"); } else { stdout().write("hostname-empty\n"); }
  putchar(72);
  putchar(105);
  putchar(10);
  return 0;
}`,
	},
	{
		name: "create_dir_all_nested_existing_and_through_a_file",
		src: `function main(): i32 {
  match (temp_dir("fernssa-cda")) {
    Ok(d) => {
      match (create_dir_all(d + "/a//b/c")) {
        Ok(u) => { stdout().write("created\n"); },
        Err(e) => { return 1; },
      }
      match (create_dir_all(d + "/a//b/c")) {
        Ok(u) => { stdout().write("exists-ok\n"); },
        Err(e) => { return 2; },
      }
      match (stat(d + "/a/b/c")) {
        Ok(s) => { if (s.is_dir) { stdout().write("is-dir\n"); } else { return 3; } },
        Err(e) => { return 4; },
      }
      match (write_file(d + "/a/file", "x")) {
        Ok(u) => {},
        Err(e) => { return 5; },
      }
      match (create_dir_all(d + "/a/file/deeper")) {
        Ok(u) => { return 6; },
        Err(e) => {
          match (e) {
            Other(p, msg) => { stdout().write("through-a-file:" + msg + "\n"); },
            _ => { return 7; },
          }
        },
      }
    },
    Err(e) => { return 8; },
  }
  return 0;
}`,
	},
	{
		// The probe reads zero in a program with no over-release.
		name: "rc_underflow_probe_reads_zero",
		src: `import "std/i32";
function main(): i32 {
  var xs: i32[] = [1, 2, 3];
  var ys: i32[] = xs;
  var s: string = "a" + "b";
  stdout().write("underflows=" + __rc_underflow_count().to_string() + " " + ys.len().to_string() + s + "\n");
  return 0;
}`,
	},
}

func TestX86_64SSASocketsMatchDefaultBackend(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)

	for _, c := range x86SSASocketCases {
		t.Run(c.name, func(t *testing.T) {
			ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, c.src, true)
			flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, c.src, false)

			if ssaCode != flatCode {
				t.Errorf("exit status: ssa=%d flat=%d", ssaCode, flatCode)
			}
			if ssaOut != flatOut {
				t.Errorf("stdout differs:\n ssa=%q\nflat=%q", ssaOut, flatOut)
			}
			if ssaErr != flatErr {
				t.Errorf("stderr differs:\n ssa=%q\nflat=%q", ssaErr, flatErr)
			}
			if ssaOut == "" {
				t.Errorf("no output at all: the program did not reach its prints")
			}
		})
	}
}
