package e2ecompiler

import (
	"bytes"
	"debug/macho"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"github.com/jakechampion/lang/internal/tools/tty"
)

func TestSelfHostArm64DarwinBuilds(t *testing.T) {
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")

	var fernBin string
	if native {
		fernBin = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	} else {
		gcc, runner := x86_64Tooling(t)
		if len(runner) != 0 {
			t.Skip("self-host CLI driver runs only natively (argv paths)")
		}
		fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	}

	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"exit_42", `function main(): i32 { return 42; }`, 42},
		{"arith", `function main(): i32 { let x = 6; let y = 7; return x * y; }`, 42},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`, 55},
		{"concat", `function main(): i32 { let s: string = "hello, " + "world!"; return s.len(); }`, 13},
		{"buf_grow", `function main(): i32 {
    let h: usize = buf_new(65536);
    let i: i32 = 0;
    while (i < 3000) { buf_push(h, "0123456789abcdefghijklmnopqrstuvwxyz"); i = i + 1; }
    let s: string = buf_take(h);
    if (s.len() != 108000) { return 1; }
    if ((s[65536] as i32) != (s[16] as i32)) { return 2; }
    if ((s[107999] as i32) != (s[35] as i32)) { return 3; }
    buf_push(h, "ok");
    let t: string = buf_take(h);
    if (t.len() != 2) { return 4; }
    return 42;
}`, 42},
		{"print", `function main(): i32 { print("hi"); return 0; }`, 0},
		{"lo12_literal", `function main(): i32 {
    let s: string = "    add x9, x9, :lo12:.Lfern_relanchor";
    if (s.len() != 38) { return 1; }
    if ((s[16] as i32) != 58) { return 2; }
    if ((s[21] as i32) != 58) { return 3; }
    if ((s[37] as i32) != 114) { return 4; }
    return 42;
}`, 42},
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { let x = Box { v: 4 }; return x.scale(3); }`, 12},
		{"array_sum", `function main(): i32 { let a = [1, 2, 3, 4, 5]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15},
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 99; }`, 42},
		{"now_unix_ms", `function main(): i32 { let t: i64 = now_unix_ms(); if (t > 1700000000000) { return 7; } return 1; }`, 7},
		{"random_bytes", `function main(): i32 { let b: u8[] = random_bytes(8); if (b.len() != 8) { return 1; } let v: i32 = 0; let i: i32 = 0; while (i < 8) { v = v | (b[i] as i32); i = i + 1; } if (v != 0) { return 7; } return 2; }`, 7},
		{"memchr", `function main(): i32 { let s = "aaaaaaaaaaaaaaaaaaaa*aaa"; return __memchr(s, 42, 0) + 22; }`, 42},
		{"ascii_run", `function main(): i32 { let s = "aaaaaaaaaaaaaaaaaaaaaaaa"; return __ascii_run(s, 0) + 18; }`, 42},
		{"const_agg_union", `struct Add { l: i32, r: i32 }
struct Lit { v: i32 }
type Expr = Add | Lit;
function eval(e: Expr): i32 { match (e) { Add(a) => { return a.l + a.r; }, Lit(l) => { return l.v; } } }
function main(): i32 { let e: Expr = Add { l: 40, r: 2 }; return eval(e); }`, 42},
	}

	darwinKnownGaps := map[string]string{}

	runCase := func(name, src string, wantExit int) {
		t.Run(name, func(t *testing.T) {
			gap, isGap := darwinKnownGaps[name]
			fail := func(format string, args ...any) {
				if isGap {
					t.Logf("known gap (%s): "+format, append([]any{gap}, args...)...)
					return
				}
				t.Errorf(format, args...)
			}

			srcPath := filepath.Join(dir, name+".fern")
			if err := os.WriteFile(srcPath, []byte(src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			binPath := filepath.Join(dir, name+".bin")
			out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", binPath, srcPath).CombinedOutput()
			if err != nil {
				if native && len(bytes.TrimSpace(out)) == 0 {
					t.Skipf("self-host CLI did not launch (err=%v, no output)", err)
				}
				fail("self-host emit failed: %v\n%s", err, out)
				return
			}

			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			f, err := macho.NewFile(bytes.NewReader(raw))
			if err != nil {
				fail("output is not a parseable Mach-O: %v", err)
				return
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				fail("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
				return
			}

			if !native {
				return
			}
			if err := os.Chmod(binPath, 0o755); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			cmd := exec.Command(binPath)
			runErr := cmd.Run()
			ps := cmd.ProcessState
			if ps == nil || !ps.Exited() {
				fail("Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
				return
			}
			if code := ps.ExitCode(); code != wantExit {
				fail("self-host arm64-darwin %q exit = %d, want %d", name, code, wantExit)
				return
			}
			if isGap && native {
				t.Errorf("%q is listed in darwinKnownGaps (%s) but now passes — delete its entry", name, gap)
			}
		})
	}

	for _, c := range cases {
		runCase(c.name, c.src, c.wantExit)
	}

	const rfContent = "hello, fern!"
	okPath := filepath.Join(dir, "rf_data.txt")
	if err := os.WriteFile(okPath, []byte(rfContent), 0o644); err != nil {
		t.Fatalf("write rf data: %v", err)
	}
	runCase("read_file_ok",
		`function main(): i32 { match (read_file("`+okPath+`")) { Ok(s) => { return s.len(); }, Err(e) => { return 99; } } }`,
		len(rfContent))
	runCase("read_file_missing",
		`function main(): i32 { match (read_file("`+filepath.Join(dir, "no_such_file_zzz")+`")) { Ok(s) => { return s.len(); }, Err(e) => { return 99; } } }`,
		99)

	wfPath := filepath.Join(dir, "wf_data.txt")
	runCase("write_file_trunc_roundtrip",
		`function main(): i32 {
  match (write_file("`+wfPath+`", "longcontent")) { Err(e) => { return 91; }, Ok(_) => {} }
  match (write_file("`+wfPath+`", "hi")) { Err(e) => { return 92; }, Ok(_) => {} }
  match (read_file("`+wfPath+`")) { Ok(s) => { return s.len(); }, Err(e) => { return 93; } }
}`,
		2)

	runCase("stat_file",
		`function main(): i32 { match (stat("`+okPath+`")) { Ok(fs) => { if (fs.is_file) { return fs.size as i32; } return 1; }, Err(e) => { return 99; } } }`,
		len(rfContent))
	runCase("stat_dir",
		`function main(): i32 { match (stat("`+dir+`")) { Ok(fs) => { if (fs.is_dir) { return 7; } return 1; }, Err(e) => { return 99; } } }`,
		7)
	runCase("stat_missing",
		`function main(): i32 { match (stat("`+filepath.Join(dir, "no_such_stat_zzz")+`")) { Ok(fs) => { return 1; }, Err(e) => { return 99; } } }`,
		99)

	runCase("statfs",
		`function main(): i32 {
  match (statfs("`+dir+`")) {
    Ok(fs) => {
      if (fs.block_size <= (0 as i64)) { return 1; }
      if (fs.block_size > (1048576 as i64)) { return 2; }
      if (fs.blocks <= (0 as i64)) { return 3; }
      if (fs.blocks_free > fs.blocks) { return 4; }
      if (fs.blocks_avail > fs.blocks_free) { return 5; }
      if (fs.files_free > fs.files) { return 6; }
      if (fs.name_max <= (0 as i64)) { return 8; }
      if (fs.name_max > (4096 as i64)) { return 9; }
      if (fs.path_max <= (0 as i64)) { return 10; }
      // Darwin has no f_frsize; the fundamental block size is f_bsize.
      if (fs.frag_size != fs.block_size) { return 13; }
      if (fs.fs_type <= (0 as i64)) { return 14; }
      match (statfs("`+filepath.Join(dir, "no_such_statfs_zzz", "x")+`")) { Ok(g) => { return 11; }, Err(e) => {} }
      return 7;
    },
    Err(e) => { return 12; }
  }
}`,
		7)

	runCase("sysctl",
		`function main(): i32 {
  match (sysctl([1, 1])) {
    Ok(bs) => {
      if (bs.len() < 6 || bs[0] != 68 as u8 || bs[5] != 110 as u8) { return 1; }
    },
    Err(e) => { return 2; }
  }
  match (sysctl([2, 2])) {
    Ok(bs) => { if (bs.len() != 24) { return 3; } },
    Err(e) => { return 4; }
  }
  return 7;
}`,
		7)

	runCase("mounts",
		`function main(): i32 {
  let root: i64 = 0 as i64;
  match (stat("/")) { Ok(st) => { root = st.dev; }, Err(e) => { return 1; } }
  match (mounts()) {
    Ok(ms) => {
      if (ms.len() == 0) { return 2; }
      for m in ms {
        if (m.target == "/") {
          if (m.dev != root) { return 3; }
          if (m.fstype.len() == 0) { return 4; }
          if (m.source.len() == 0) { return 5; }
          return 7;
        }
      }
      return 6;
    },
    Err(e) => { return 8; }
  }
}`,
		7)

	t.Run("window_size", func(t *testing.T) {
		srcPath := filepath.Join(dir, "window_size.fern")
		if err := os.WriteFile(srcPath, []byte(`function main(): i32 {
  match (window_size(1)) {
    Ok(ws) => {
      if (ws.rows != (13 as i64)) { return 21; }
      if (ws.cols != (57 as i64)) { return 22; }
      return 7;
    },
    Err(e) => { return 23; }
  }
}
`), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		binPath := filepath.Join(dir, "window_size.bin")
		if out, err := exec.Command(fernBin, "-target", "arm64-darwin", "-o", binPath, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("self-host emit failed: %v\n%s", err, out)
		}
		if !native {
			return
		}
		if err := os.Chmod(binPath, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		master, slave, err := tty.OpenPTY()
		if err != nil {
			t.Fatalf("OpenPTY: %v", err)
		}
		defer master.Close()
		if err := tty.SetWindowSize(int(slave.Fd()), 13, 57); err != nil {
			t.Fatalf("set pty size: %v", err)
		}
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, master)
			close(done)
		}()
		cmd := exec.Command(binPath)
		cmd.Stdout = slave
		_ = cmd.Run()
		slave.Close()
		<-done
		ps := cmd.ProcessState
		if ps == nil || !ps.Exited() {
			t.Fatalf("Mach-O did not run to a normal exit (state=%v)", ps)
		}
		if code := ps.ExitCode(); code != 7 {
			t.Errorf("on a 13x57 pty: exit = %d, want 7 (21 = wrong rows, 22 = wrong cols, 23 = Err)", code)
		}
	})

	rmPath := filepath.Join(dir, "rm_target.txt")
	runCase("remove_file_lifecycle",
		`function main(): i32 {
  match (write_file("`+rmPath+`", "x")) { Err(e) => { return 1; }, Ok(_) => {} }
  match (remove_file("`+rmPath+`")) { Err(e) => { return 2; }, Ok(_) => {} }
  match (stat("`+rmPath+`")) { Ok(fs) => { return 3; }, Err(e) => { return 7; } }
}`,
		7)

	runCase("monotonic_ns",
		`function main(): i32 { let a: i64 = monotonic_ns(); let b: i64 = monotonic_ns(); if (b >= a) { if (a > 0) { return 7; } } return 1; }`,
		7)

	runCase("temp_dir",
		`function main(): i32 { match (temp_dir("fern-darwin-test")) { Ok(d) => { if (d.len() > 0) { return 7; } return 1; }, Err(e) => { return 2; } } }`,
		7)

	runCase("fs_builtins_lifecycle", fsBuiltinsProgram, 42)

	chmodPath := filepath.Join(dir, "chmod_target.txt")
	runCase("chmod_roundtrip",
		`function main(): i32 {
  match (write_file("`+chmodPath+`", "x")) { Err(e) => { return 1; }, Ok(_) => {} }
  match (chmod("`+chmodPath+`", 2541)) { Err(e) => { return 2; }, Ok(_) => {} }
  match (stat("`+chmodPath+`")) { Ok(f) => { if ((f.mode & (4095 as u32)) != (2541 as u32)) { return 3; } }, Err(e) => { return 4; } }
  match (chmod("`+filepath.Join(dir, "no_such_chmod_zzz")+`", 420)) { Ok(_) => { return 5; }, Err(e) => {} }
  return 7;
}`,
		7)

	sftPath := filepath.Join(dir, "sft_target.txt")
	runCase("set_file_times_roundtrip",
		`function main(): i32 {
  match (write_file("`+sftPath+`", "x")) { Err(e) => { return 1; }, Ok(_) => {} }
  match (set_file_times("`+sftPath+`", 1111111111, 222333444, 1444555666, 777888999, 0)) { Err(e) => { return 2; }, Ok(_) => {} }
  match (stat("`+sftPath+`")) {
    Ok(f) => {
      if (f.atime != (1111111111 as i64)) { return 3; }
      if (f.atime_nsec != (222333444 as i64)) { return 4; }
      if (f.mtime != (1444555666 as i64)) { return 5; }
      if (f.mtime_nsec != (777888999 as i64)) { return 6; }
    },
    Err(e) => { return 7; }
  }
  match (set_file_times("`+sftPath+`", 9, 9, 1777888999, 864213579, 2)) { Err(e) => { return 8; }, Ok(_) => {} }
  match (stat("`+sftPath+`")) {
    Ok(f) => {
      if (f.atime != (1111111111 as i64)) { return 9; }
      if (f.mtime != (1777888999 as i64)) { return 10; }
      if (f.mtime_nsec != (864213579 as i64)) { return 11; }
    },
    Err(e) => { return 12; }
  }
  match (set_file_times("`+filepath.Join(dir, "no_such_sft_zzz")+`", 1, 0, 1, 0, 0)) { Ok(_) => { return 13; }, Err(e) => {} }
  match (set_file_times("`+sftPath+`", 5, 5, 5, 5, 12)) { Err(e) => { return 14; }, Ok(_) => {} }
  match (stat("`+sftPath+`")) {
    Ok(f) => {
      if (f.atime <= (1750000000 as i64)) { return 15; }
      if (f.mtime != (1777888999 as i64)) { return 16; }
    },
    Err(e) => { return 17; }
  }
  match (set_file_times("`+sftPath+`", 5, 5, 5, 5, 24)) { Err(e) => { return 18; }, Ok(_) => {} }
  match (stat("`+sftPath+`")) {
    Ok(f) => {
      if (f.atime <= (1750000000 as i64)) { return 19; }
      if (f.mtime <= (1750000000 as i64)) { return 20; }
    },
    Err(e) => { return 21; }
  }
  return 42;
}`,
		42)

	runCase("sleep_ms",
		`function main(): i32 { sleep_ms(5); return 7; }`,
		7)

	runCase("sleep_ns",
		`function main(): i32 { sleep_ns(5000000 as i64); sleep_ns(400 as i64); `+
			`let t0: i64 = monotonic_ns(); sleep_ns(999999500 as i64); `+
			`if (monotonic_ns() - t0 < (999999500 as i64)) { return 1; } return 7; }`,
		7)

	runCase("process_alive",
		`function main(): i32 {
  if (!process_alive(1)) { return 90; }
  if (process_alive(4194304)) { return 91; }
  if (process_alive(0)) { return 92; }
  if (process_alive(0 - 1)) { return 93; }
  return 7;
}`,
		7)

	runCase("rlimit_nofile",
		`function main(): i32 {
  let n: i64 = rlimit_nofile();
  if (n < (4 as i64)) { return 90; }
  if (n > (2147483647 as i64)) { return 91; }
  return 7;
}`,
		7)

	runCase("subprocess_echo",
		`function main(): i32 {
  let r: ProcessResult = subprocess("echo", ["hi"], "");
  if (r.exit_code != 0) { return 90; }
  return r.stdout.len();
}`,
		3)

	runCase("subprocess_exit_code",
		`function main(): i32 {
  let r: ProcessResult = subprocess("sh", ["-c", "exit 7"], "");
  return r.exit_code;
}`,
		7)

	runCase("subprocess_stdin",
		`function main(): i32 {
  let r: ProcessResult = subprocess("cat", [], "piped");
  if (r.exit_code != 0) { return 90; }
  return r.stdout.len();
}`,
		5)

	runCase("subprocess_missing",
		`function main(): i32 {
  let r: ProcessResult = subprocess("fern-no-such-binary-zzz-7349", [], "");
  if (r.stderr != "fern-no-such-binary-zzz-7349: No such file or directory\n") { return 90; }
  return r.exit_code;
}`,
		127)

	runCase("tcp_listen_close",
		`function main(): i32 {
  let fd: i32 = tcp_listen(39517);
  if (fd < 0) { return 1; }
  if (tcp_close(fd) < 0) { return 2; }
  return 7;
}`,
		7)

	runCase("udp_sendto",
		`function main(): i32 {
  let s: i32 = udp_bind([0u8, 0u8, 0u8, 0u8], 0);
  if (s < 0) { return 1; }
  let n: i32 = udp_sendto(s, [127u8, 0u8, 0u8, 1u8], 39518, "abc");
  if (tcp_close(s) < 0) { return 2; }
  return n;
}`,
		3)
}

func buildSelfHostBinArm64Darwin(t *testing.T, dir, fernName, out string) string {
	t.Helper()
	return buildSelfHostBinFor(t, dir, fernName, out, e2eharness.TargetArm64Darwin)
}
