package e2e

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// hasLoadCommand reports whether a Mach-O image contains a load command
// with the given cmd id (walking the raw header, little-endian arm64).
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

// TestArm64DarwinNativeMachO builds an integer program through the
// in-process Mach-O backend (`-target arm64-darwin -native`, no clang/
// ld64) and validates it. Off Apple Silicon it only checks the file is a
// well-formed signed Mach-O. On the macOS arm64 CI runner it also EXECUTES
// the binary — the decisive test of whether a static, dyld-free,
// LC_UNIXTHREAD + ad-hoc-signed executable launches on current macOS.
//
// Launch failure (the binary is rejected by the kernel) is reported as a
// skip with diagnostics rather than a hard failure: it's an open question
// answered by this very run, not a regression. A wrong exit code — the
// binary ran but misbehaved — is a hard failure.
func TestArm64DarwinNativeMachO(t *testing.T) {
	bin := buildFernCLI(t)
	cases := []struct {
		name     string
		src      string
		wantExit int
	}{
		// Integer only — exercises just the code path (no __DATA).
		{"exit", `function main(): i32 { return 42; }`, 42},
		// String constant — exercises __DATA, adrp @PAGE / @PAGEOFF to a
		// read-only string, and the write(2) syscall.
		{"print", `
function main(): i32 { print("hi"); return 0; }`, 0},
		// Heap allocation — exercises the writable globals in __DATA
		// (__fern_heap_ptr etc.) and mmap via svc.
		{"concat", `
import "std/string";
function main(): i32 { var a: string = "foo"; return (a + "bar").len(); }`, 6},
		// now_unix_ms — exercises the Darwin gettimeofday port (vs Linux
		// clock_gettime). A plausible wall-clock value (post-2023) → 7.
		{"now_unix_ms", `function main(): i32 {
  var t: i64 = now_unix_ms();
  if (t > 1700000000000) { return 7; }
  return 1;
}`, 7},
		// random_bytes — exercises the Darwin getentropy port (vs Linux
		// getrandom). Asserts length AND that the bytes are actually
		// filled by the syscall: OR-of-8-bytes==0 would mean the buffer
		// is still the zero-mapped alloc memory (syscall failed silently).
		{"random_bytes", `
function main(): i32 {
  var b: string = random_bytes(8);
  if (b.len() != 8) { return 1; }
  var v: i32 = 0; var i: i32 = 0;
  while (i < 8) { v = v | (b[i] as i32); i = i + 1; }
  if (v != 0) { return 7; }
  return 2;
}`, 7},
		// Map with HEAP-allocated string values — the arm64-darwin
		// >4 GiB pointer-truncation regression guard (docs/BACKEND-PARITY.md
		// "Known limitations"). The keys/values are built by concat (`"a" +
		// "b"`), so they live on the heap, which macOS-14+ maps above 4 GiB —
		// where a 32-bit Map value slot truncated the pointer and the lookup
		// read garbage. The core/map runtime is now usize-pointered
		// throughout, so the values round-trip; exit 42 proves it. (On
		// Linux/x86 the heap is low so this passes trivially — it only
		// exercises the truncation on a real Apple Silicon runner.)
		{"map_heap_string_values", `
import "core/map";
function main(): i32 {
  var m: Map[string, string] = map_new(8);
  m = m.insert("key" + "_one", "value" + "_one");
  m = m.insert("key" + "_two", "value" + "_two");
  if (m.get_or("key_one", "x") != "value_one") { return 1; }
  if (m.get_or("key_two", "x") != "value_two") { return 2; }
  if (m.len() != 2) { return 3; }
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
			// No -native: arm64-darwin defaults to the in-process backend.
			if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("default arm64-darwin build failed: %v\n%s", err, o)
			}

			// Structural validation (runs on every host).
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
			// The in-process backend's marker is an ad-hoc LC_CODE_SIGNATURE
			// (0x1D) with no LC_DYLD_EXPORTS_TRIE: clang/ld64 emits the trie
			// and a UUID, and this writer emits neither. Its presence proves
			// the DEFAULT is native, with no external toolchain. (The old
			// marker was LC_UNIXTHREAD, which this image no longer has — it
			// is dyld-loaded now, because nothing else launches.)
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
				// Was a skip while it was an open question whether Apple
				// Silicon would launch this image at all. It does — the image
				// is dyld-loaded, PIE and ad-hoc signed — so a launch failure
				// is now a regression in the container, not a platform limit.
				t.Fatalf("native Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
			}
			if code := ps.ExitCode(); code != c.wantExit {
				t.Errorf("native arm64-darwin %q exit = %d, want %d", c.name, code, c.wantExit)
			}
		})
	}
}

// TestArm64DarwinNativeReadFile validates the Darwin read_file port — the
// fstat64 syscall (339) and st_size at struct-stat offset 96 — by reading
// a known file and checking its length. Builds everywhere; executes only
// on Apple Silicon.
func TestArm64DarwinNativeReadFile(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.txt")
	const content = "hello, fern!" // 12 bytes
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

// TestArm64DarwinDwarfSymtab guards the -g static symbol table for the
// arm64-darwin native path (#5537 slice 1, Mach-O): a `-g` build carries an
// LC_SYMTAB whose entries resolve every function name, while the default build
// has none (keeping default binaries small). Structural check — builds and
// parses on every host; no execution needed.
func TestArm64DarwinDwarfSymtab(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := "function helper(x: i32): i32 { return x * 2; }\n" +
		"function main(): i32 { return helper(21); }\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// Default build: no symbol table.
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

	// -g build: LC_SYMTAB naming the functions.
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
	for _, name := range []string{"main", "helper"} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing symbol %q in -g build (have %v)", name, got)
		}
	}
	// Every function symbol lands inside the __text section's address range.
	if ts := gf.Section("__text"); ts != nil {
		lo, hi := ts.Addr, ts.Addr+ts.Size
		for name, v := range got {
			if v < lo || v >= hi {
				t.Errorf("symbol %q @%#x outside __text [%#x,%#x)", name, v, lo, hi)
			}
		}
	}
}

// TestArm64DarwinCcOptsOut confirms -cc still routes arm64-darwin through
// an external toolchain: a failing -cc must make the build fail, proving
// the default path doesn't shell out. Host-independent.
func TestArm64DarwinCcOptsOut(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	if err := exec.Command(bin, "-target", "arm64-darwin", "-cc", "/bin/false", "-o", out, src).Run(); err == nil {
		t.Errorf("expected build to fail when -cc points at a failing linker, but it succeeded")
	}
}
