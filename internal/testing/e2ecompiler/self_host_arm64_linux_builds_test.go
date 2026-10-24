package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostArm64LinuxBuilds(t *testing.T) {
	if _, err := exec.LookPath("qemu-aarch64"); err != nil {
		t.Skip("qemu-aarch64 not on PATH; skipping arm64-linux self-host e2e")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI driver runs only natively (argv paths)")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct {
		name     string
		src      string
		wantExit int
		wantOut  string
	}{
		{"exit_42", `function main(): i32 { return 42; }`, 42, ""},
		{"arith", `function main(): i32 { let x = 6; let y = 7; return x * y; }`, 42, ""},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); } function main(): i32 { return fib(10); }`, 55, ""},
		{"loop_sum", `function main(): i32 { let s: i32 = 0; let i: i32 = 1; while (i <= 10) { s = s + i; i = i + 1; } return s; }`, 55, ""},
		{"concat", `function main(): i32 { let s: string = "hello, " + "world!"; return s.len(); }`, 13, ""},
		{"print", `function main(): i32 { print("hi"); return 0; }`, 0, "hi\n"},
		{"print_literal", `function main(): i32 { print("Hello, Fern!"); return 0; }`, 0, "Hello, Fern!\n"},
		{"print_concat", `function main(): i32 { let s: string = "Hello, " + "Fern!"; print(s); return 0; }`, 0, "Hello, Fern!\n"},
		{"index_byte", `function main(): i32 { let s: string = "abcdef"; return (s[3] as i32) - 90; }`, 10, ""},
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
}`, 42, ""},
		{"struct_method", `struct Box { v: i32 } function (b: Box) scale(n: i32): i32 { return b.v * n; } function main(): i32 { let x = Box { v: 4 }; return x.scale(3); }`, 12, ""},
		{"array_sum", `function main(): i32 { let a = [1, 2, 3, 4, 5]; let i = 0; let s = 0; while (i < a.len()) { s = s + a[i]; i = i + 1; } return s; }`, 15, ""},
		{"option", `function pick(n: i32): Option[i32] { if (n == 0) { return None; } return Some(n + 1); } function main(): i32 { match (pick(41)) { Some(v) => { return v; }, None => { return 0; } } return 99; }`, 42, ""},
		{"enum", `enum Shape { Circle(i32), Square(i32) } function area(s: Shape): i32 { match (s) { Circle(r) => { return r*r*3; }, Square(w) => { return w*w; } } } function main(): i32 { return area(Circle(2)) + area(Square(3)); }`, 21, ""},
		{"floats", `function main(): i32 { let x: f64 = 3.5; let y: f64 = 2.0; let z: f64 = x*y + x/y - x; if (z > 5.0) { return 7; } return 1; }`, 7, ""},
		{"bitcount", `function main(): i32 {
    if (__ctz32(0 as u32) != 32) { return 1; }
    if (__ctz64(0 as u64) != 64) { return 2; }
    if (__popcount32(0 as u32) != 0) { return 3; }
    if (__popcount64(0 as u64) != 0) { return 4; }
    if (__ctz32(16 as u32) != 4) { return 5; }
    if (__ctz64(1048576 as u64) != 20) { return 6; }
    if (__popcount32(4294967295 as u32) != 32) { return 7; }
    if (__popcount64(1023 as u64) != 10) { return 8; }
    if (__clz32(1 as u32) != 31) { return 9; }
    if (__clz64(1 as u64) != 63) { return 10; }
    return 42;
}`, 42, ""},
		{"tcp_listen_close_no_alloc", `function main(): i32 {
  let fd: i32 = tcp_listen(0);
  if (fd < 0) { return 1; }
  if (tcp_close(fd) < 0) { return 2; }
  return 42;
}`, 42, ""},
		{"tcp_local_port_no_alloc", `function main(): i32 {
  let fd: i32 = tcp_listen(0);
  if (fd < 0) { return 1; }
  let port: i32 = tcp_local_port(fd);
  if (tcp_close(fd) < 0) { return 2; }
  if (port <= 0) { return 3; }
  if (port > 65535) { return 4; }
  return 42;
}`, 42, ""},
		{"wide_frame", wideFrameSource(8400), 42, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcPath := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(srcPath, []byte(c.src+"\n"), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			binPath := filepath.Join(dir, c.name+".bin")
			if out, err := exec.Command(fernBin, "-target", "arm64-linux", "-o", binPath, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host emit (arm64 ELF) failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read bin: %v", err)
			}
			f, err := elf.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("output is not a parseable ELF: %v", err)
			}
			if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
				t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
			}
			fi, err := os.Stat(binPath)
			if err != nil {
				t.Fatalf("stat output: %v", err)
			}
			if fi.Mode().Perm()&0o111 == 0 {
				t.Fatalf("self-host wrote a NON-EXECUTABLE binary (mode %04o); "+
					"the CLI must write_file_exec for a target that emits one", fi.Mode().Perm())
			}
			cmd := exec.Command("qemu-aarch64", binPath)
			var so, se bytes.Buffer
			cmd.Stdout, cmd.Stderr = &so, &se
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != c.wantExit {
				t.Errorf("self-host arm64 %q exit = %d, want %d (stderr: %s)", c.name, code, c.wantExit, se.String())
			}
			if c.wantOut != "" && so.String() != c.wantOut {
				t.Errorf("self-host arm64 %q stdout = %q, want %q", c.name, so.String(), c.wantOut)
			}
		})
	}
}

func wideFrameSource(n int) string {
	var b bytes.Buffer
	b.WriteString("function main(): i32 {\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "    let v%d: i32 = %d;\n", i, i)
	}
	fmt.Fprintf(&b, "    return v%d - v%d;\n}\n", n-1, n-43)
	return b.String()
}
