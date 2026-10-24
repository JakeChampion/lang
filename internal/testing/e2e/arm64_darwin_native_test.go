package e2e

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tables/symname"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func hasLoadCommand(bin []byte, cmd uint32) bool {
	if len(bin) < 20 {
		return false
	}
	ncmds := binary.LittleEndian.Uint32(bin[16:])
	off := 32
	for i := uint32(0); i < ncmds && off+8 <= len(bin); i++ {
		c := binary.LittleEndian.Uint32(bin[off:])
		sz := binary.LittleEndian.Uint32(bin[off+4:])
		if c == cmd {
			return true
		}
		if sz == 0 {
			break
		}
		off += int(sz)
	}
	return false
}

func TestArm64DarwinNativeMachO(t *testing.T) {
	bin := buildFernCLI(t)
	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		{"exit", `function main(): i32 { return 42; }`, 42},
		{"callback_snapshot", callbackSnapshotProg, 42},
		{"print", `
function main(): i32 { print("hi"); return 0; }`, 0},
		{"concat", `
import "std/string";
function main(): i32 { let a: string = "foo"; return (a + "bar").len(); }`, 6},
		{"now_unix_ms", `function main(): i32 {
  let t: i64 = now_unix_ms();
  if (t > 1700000000000) { return 7; }
  return 1;
}`, 7},
		{"random_bytes", `
function main(): i32 {
  let b: u8[] = random_bytes(8);
  if (b.len() != 8) { return 1; }
  let v: i32 = 0; let i: i32 = 0;
  while (i < 8) { v = v | (b[i] as i32); i = i + 1; }
  if (v != 0) { return 7; }
  return 2;
}`, 7},
		{"lstat_nofollow_flag", `
function main(): i32 {
  let d: string = "";
  match (temp_dir("lst")) { Ok(p) => { d = p; }, Err(_) => { return 1; } }
  let f: string = d + "/f";
  match (write_file(f, "x")) { Ok(_) => {}, Err(_) => { return 2; } }
  let l: string = d + "/l";
  match (create_symlink(f, l)) { Ok(_) => {}, Err(_) => { return 3; } }
  match (lstat(f)) { Ok(s) => { if (!s.is_file) { return 4; } }, Err(_) => { return 5; } }
  match (lstat(l)) {
    Ok(s) => { if (s.is_file) { return 6; } if (s.is_dir) { return 7; } },
    Err(_) => { return 8; }
  }
  match (stat(l)) { Ok(s) => { if (!s.is_file) { return 9; } }, Err(_) => { return 10; } }
  return 42;
}`, 42},
		{"map_heap_string_values", `
import "core/map";
function main(): i32 {
  let m: Map[string, string] = map_new(8);
  m = m.insert("key" + "_one", "value" + "_one");
  m = m.insert("key" + "_two", "value" + "_two");
  if (m.get_or("key_one", "x") != "value_one") { return 1; }
  if (m.get_or("key_two", "x") != "value_two") { return 2; }
  if (m.len() != 2) { return 3; }
  return 42;
}`, 42},
		{"poll_kqueue_readiness", `
function main(): i32 {
  let port: i32 = 18475;
  let listener: i32 = tcp_listen(port);
  if (listener < 0) { return 90; }
  let c: i32 = tcp_connect_with([127u8, 0u8, 0u8, 1u8], port, false);
  if (c < 0) { return 91; }
  let fds: i32[] = [0 - 1, listener];
  if (poll(fds, 2000) != 1) { return 99; }
  return 42;
}`, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "prog.fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, "prog")
			if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("default arm64-darwin build failed: %v\n%s", err, o)
			}

			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read out: %v", err)
			}
			f, err := macho.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("output is not a parseable Mach-O: %v", err)
			}
			if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
				t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
			}
			if !hasLoadCommand(raw, 0x1D) {
				t.Errorf("default build is missing LC_CODE_SIGNATURE — expected the native backend, not clang")
			}
			if hasLoadCommand(raw, 0x80000033) {
				t.Errorf("default build has LC_DYLD_EXPORTS_TRIE — that is clang/ld64 output, not the native backend")
			}

			if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
				t.Skip("execution check only runs on Apple Silicon")
			}

			cmd := exec.Command(out)
			runErr := cmd.Run()
			ps := cmd.ProcessState
			if ps == nil || !ps.Exited() {
				t.Fatalf("native Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
			}
			if code := ps.ExitCode(); code != c.wantExit {
				t.Errorf("native arm64-darwin %q exit = %d, want %d", c.name, code, c.wantExit)
			}
		})
	}
}

func TestArm64DarwinNativeReadFileRelative(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	const content = "hello, fern!"
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	src := filepath.Join(dir, "prog.fern")
	prog := "function main(): i32 {\n  match (read_file(\"data.txt\")) {\n    Ok(s) => { return s.len(); },\n    Err(e) => { return 99; }\n  }\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	cmd := exec.Command(out)
	cmd.Dir = dir
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
	}
	if code := ps.ExitCode(); code != len(content) {
		t.Errorf("read_file(\"data.txt\").len() = %d, want %d (99 = the Err arm: AT_FDCWD wrong for the target?)", code, len(content))
	}
}

func TestArm64DarwinNativeReadFile(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.txt")
	const content = "hello, fern!"
	if err := os.WriteFile(dataPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	src := filepath.Join(dir, "prog.fern")
	prog := "function main(): i32 {\n  match (read_file(\"" + dataPath + "\")) {\n    Ok(s) => { return s.len(); },\n    Err(e) => { return 99; }\n  }\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if _, err := macho.NewFile(bytes.NewReader(raw)); err != nil {
		t.Fatalf("output is not a parseable Mach-O: %v", err)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	cmd := exec.Command(out)
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
	}
	if code := ps.ExitCode(); code != len(content) {
		t.Errorf("read_file().len() = %d, want %d (fstat64 / st_size offset wrong?)", code, len(content))
	}
}

func TestArm64DarwinMonotonicNs(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := "function main(): i32 {\n" +
		"    let t0: i64 = monotonic_ns();\n" +
		"    if (t0 <= (0 as i64)) { return 91; }\n" +
		"    sleep_ms(50);\n" +
		"    let t1: i64 = monotonic_ns();\n" +
		"    let ms: i64 = (t1 - t0) / (1000000 as i64);\n" +
		"    if (ms < (25 as i64)) { return 92; }\n" +
		"    if (ms > (5000 as i64)) { return 93; }\n" +
		"    return 0;\n" +
		"}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	cmd := exec.Command(out)
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
	}
	switch code := ps.ExitCode(); code {
	case 0:
	case 91:
		t.Error("monotonic_ns() returned <= 0 (CNTVCT_EL0 not read?)")
	case 92, 93:
		t.Errorf("50 ms sleep measured outside 25..5000 ms (exit %d): CNTFRQ_EL0 scaling wrong?", code)
	default:
		t.Errorf("unexpected exit code %d", code)
	}
}

func TestArm64DarwinSleepNsCarry(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := "function main(): i32 {\n" +
		"    let t0: i64 = monotonic_ns();\n" +
		"    sleep_ns(999999500 as i64);\n" +
		"    if (monotonic_ns() - t0 < (999999500 as i64)) { return 94; }\n" +
		"    return 0;\n" +
		"}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	cmd := exec.Command(out)
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
	}
	switch code := ps.ExitCode(); code {
	case 0:
	case 94:
		t.Error("sleep_ns(999999500) returned early: tv_usec reached 1000000 and select rejected the timeval")
	default:
		t.Errorf("unexpected exit code %d", code)
	}
}

func TestArm64DarwinDwarfSymtab(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := "@noinline function helper(x: i32): i32 { return x * 2; }\n" +
		"function main(): i32 { return helper(21); }\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	plain := filepath.Join(dir, "plain")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", plain, src).CombinedOutput(); err != nil {
		t.Fatalf("default build: %v\n%s", err, o)
	}
	pf, err := macho.Open(plain)
	if err != nil {
		t.Fatalf("parse default: %v", err)
	}
	if pf.Symtab != nil && len(pf.Symtab.Syms) > 0 {
		t.Errorf("default build should have no symbols, got %d", len(pf.Symtab.Syms))
	}

	g := filepath.Join(dir, "g")
	if o, err := exec.Command(bin, "-g", "-target", "arm64-darwin", "-o", g, src).CombinedOutput(); err != nil {
		t.Fatalf("-g build: %v\n%s", err, o)
	}
	gf, err := macho.Open(g)
	if err != nil {
		t.Fatalf("parse -g: %v", err)
	}
	if gf.Symtab == nil {
		t.Fatal("-g build has no LC_SYMTAB")
	}
	got := map[string]uint64{}
	for _, s := range gf.Symtab.Syms {
		got[s.Name] = s.Value
	}
	for _, fn := range []string{"main", "helper"} {
		if _, ok := got[symname.Fn(fn)]; !ok {
			t.Errorf("missing symbol %q in -g build (have %v)", symname.Fn(fn), got)
		}
	}
	if ts := gf.Section("__text"); ts != nil {
		lo, hi := ts.Addr, ts.Addr+ts.Size
		for name, v := range got {
			if v < lo || v >= hi {
				t.Errorf("symbol %q @%#x outside __text [%#x,%#x)", name, v, lo, hi)
			}
		}
	}
}

func TestArm64DarwinEhFrame(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }\n" +
		"function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }\n" +
		"function main(): i32 { return fib(9) + fact(1); }\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-g", "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}
	f, err := macho.Open(out)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	defer f.Close()
	sec := f.Section("__eh_frame")
	if sec == nil {
		t.Fatal("no __TEXT,__eh_frame in the image — the emitter's CFI was recorded and discarded")
	}
	if sec.Seg != "__TEXT" {
		t.Fatalf("__eh_frame is in %s, want __TEXT", sec.Seg)
	}
	eh, err := sec.Data()
	if err != nil {
		t.Fatal(err)
	}
	var fdes [][2]uint64
	for off := 0; off+4 <= len(eh); {
		n := int(binary.LittleEndian.Uint32(eh[off:]))
		if n == 0 {
			break
		}
		if off+4+n > len(eh) {
			t.Fatalf("entry at %#x claims %d bytes past the end of a %d-byte __eh_frame", off, n, len(eh))
		}
		body := eh[off+4 : off+4+n]
		if binary.LittleEndian.Uint32(body) != 0 {
			field := sec.Addr + uint64(off) + 8
			start := uint64(int64(field) + int64(binary.LittleEndian.Uint64(body[4:])))
			fdes = append(fdes, [2]uint64{start, start + binary.LittleEndian.Uint64(body[12:])})
		}
		off += 4 + n
	}
	if f.Symtab == nil {
		t.Fatal("-g build has no LC_SYMTAB")
	}
	var syms []macho.Symbol
	for _, s := range f.Symtab.Syms {
		syms = append(syms, s)
	}
	sort.Slice(syms, func(i, j int) bool { return syms[i].Value < syms[j].Value })
	text := f.Section("__text")
	next := func(i int) uint64 {
		if i+1 < len(syms) {
			return syms[i+1].Value
		}
		return text.Addr + text.Size
	}
	want := map[uint64]string{}
	ends := map[uint64]uint64{}
	for i, s := range syms {
		ends[s.Value] = next(i)
		if strings.HasPrefix(s.Name, "__fn_") {
			want[s.Value] = s.Name
		}
	}
	if len(want) != 3 {
		t.Fatalf("found %d user functions, want the 3 the fixture declares: %v", len(want), want)
	}
	covered := map[uint64]bool{}
	for _, r := range fdes {
		end, ok := ends[r[0]]
		if !ok {
			t.Errorf("an FDE covers [%#x, %#x), which does not start at any symbol", r[0], r[1])
			continue
		}
		if r[1] <= r[0] || r[1] > end {
			t.Errorf("the FDE at %#x ends at %#x, past the next symbol at %#x — a stale range that was never remapped", r[0], r[1], end)
		}
		if covered[r[0]] {
			t.Errorf("two FDEs describe the function at %#x", r[0])
		}
		covered[r[0]] = true
	}
	for at, name := range want {
		if !covered[at] {
			t.Errorf("%s at %#x has no FDE — unwinding stops there", name, at)
		}
	}

	dump, err := exec.LookPath("llvm-dwarfdump")
	if err != nil {
		t.Skip("llvm-dwarfdump not on PATH; FDE ranges checked structurally only")
	}
	o, err := exec.Command(dump, "--eh-frame", out).Output()
	if err != nil {
		t.Fatalf("llvm-dwarfdump rejects the image's __eh_frame: %v", err)
	}
	for _, w := range []string{
		"Return address column: 30",
		"DW_CFA_def_cfa_offset: +16",
		"DW_CFA_offset: W29 -16",
		"DW_CFA_offset: W30 -8",
		"DW_CFA_def_cfa_register: W29",
	} {
		if !strings.Contains(string(o), w) {
			t.Errorf("decoded __eh_frame is missing %q", w)
		}
	}
	if n := strings.Count(string(o), " FDE "); n != len(fdes) {
		t.Errorf("llvm-dwarfdump decodes %d FDEs, the walk found %d", n, len(fdes))
	}
}

func TestArm64DarwinNativeOpenWith(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("runs the binary; Apple Silicon only")
	}
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(openWithSource(dir)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	cmd := exec.Command(out)
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("native Mach-O did not run to a normal exit (state=%v)", ps)
	}
	if code := ps.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see openWithSource)", code)
	}
	openWithCheckTree(t, dir)
}

func TestArm64DarwinNativePriority(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("runs the binary; Apple Silicon only")
	}
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(prioritySource()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	cmd := exec.Command(out)
	_ = cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("native Mach-O did not run to a normal exit (state=%v) — a SIGSYS here is a syscall number XNU does not have", ps)
	}
	if code := ps.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see prioritySource)", code)
	}
}

func TestArm64DarwinNativeTrailingSlash(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(e2eharness.TrailingSlashSource), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("execution check only runs on Apple Silicon")
	}
	e2eharness.CheckTrailingSlash(t, exec.Command(out), e2eharness.TrailingSlashTree(t))
}
