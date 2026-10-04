package e2eselfhost

import (
	"debug/macho"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/symname"
)

// `-g` on arm64-darwin writes the function labels into the Mach-O's LC_SYMTAB
// (#11409), the counterpart of the ELF targets' .symtab (#6637): nm, lldb and
// a crash backtrace name a code address by them. A default build keeps the
// degenerate LC_SYMTAB dyld requires and no symbols. The check is on the
// file's structure, so it needs no Apple Silicon host.
func TestSelfHostArm64DarwinSymtab(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := "@noinline function helper(x: i32): i32 { return x * 2; }\nfunction main(): i32 { return helper(21); }\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(out string, flags ...string) *macho.File {
		t.Helper()
		args := append(append([]string{}, flags...), "-target", "arm64-darwin", "-o", out, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %v: %v\n%s", flags, err, o)
		}
		f, err := macho.Open(out)
		if err != nil {
			t.Fatalf("parse %s: %v", out, err)
		}
		return f
	}

	plain := build(filepath.Join(dir, "plain"))
	if plain.Symtab != nil && len(plain.Symtab.Syms) > 0 {
		t.Errorf("default build carries %d symbols, want none", len(plain.Symtab.Syms))
	}

	g := build(filepath.Join(dir, "g"), "-g")
	if g.Symtab == nil {
		t.Fatal("-g build has no LC_SYMTAB")
	}
	got := map[string]uint64{}
	for _, s := range g.Symtab.Syms {
		got[s.Name] = s.Value
	}
	for _, fn := range []string{"main", "helper"} {
		if _, ok := got[symname.Fn(fn)]; !ok {
			t.Errorf("missing symbol %q in the -g build (have %v)", symname.Fn(fn), got)
		}
	}
	text := g.Section("__text")
	if text == nil {
		t.Fatal("no __text section")
	}
	var prev uint64
	for i, s := range g.Symtab.Syms {
		if s.Value < text.Addr || s.Value >= text.Addr+text.Size {
			t.Errorf("symbol %q @%#x outside __text [%#x,%#x)", s.Name, s.Value, text.Addr, text.Addr+text.Size)
		}
		if i > 0 && s.Value < prev {
			t.Errorf("symbol %q @%#x is out of address order after %#x", s.Name, s.Value, prev)
		}
		prev = s.Value
		if s.Name == "" || s.Name[0] == 'L' || s.Name[0] == '.' {
			t.Errorf("local label %q leaked into the symbol table", s.Name)
		}
		if s.Sect != 1 || s.Type != 0x0f {
			t.Errorf("symbol %q: n_sect %d n_type %#x, want section 1 (__text) and N_SECT|N_EXT", s.Name, s.Sect, s.Type)
		}
	}
	if g.Dysymtab == nil || int(g.Dysymtab.Nlocalsym) != len(g.Symtab.Syms) {
		t.Errorf("LC_DYSYMTAB's local range does not cover the %d symbols: %+v", len(g.Symtab.Syms), g.Dysymtab)
	}
}
