package e2e

import (
	goelf "debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/tables/symname"
)

func TestX86_64DebugSymtab(t *testing.T) {
	src := `@noinline function helper(n: i32): i32 { return n * 3; }
function main(): i32 { return helper(14); }
`
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "sym.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "sym.bin")
	if o, err := exec.Command(bin, "-g", "-target", "x86-64-linux", "-o", out, p).CombinedOutput(); err != nil {
		t.Fatalf("x86-64 -g build: %v\n%s", err, o)
	}

	f, err := goelf.Open(out)
	if err != nil {
		t.Fatalf("open ELF: %v", err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatalf("Symbols(): %v (expected a .symtab under -g)", err)
	}
	found := map[string]goelf.Symbol{}
	for _, s := range syms {
		found[s.Name] = s
	}
	for _, fn := range []string{"main", "helper"} {
		name := symname.Fn(fn)
		s, ok := found[name]
		if !ok {
			t.Errorf("missing symbol %q in -g binary", name)
			continue
		}
		if goelf.ST_TYPE(s.Info) != goelf.STT_FUNC {
			t.Errorf("%s: type = %v, want STT_FUNC", name, goelf.ST_TYPE(s.Info))
		}
		if s.Value == 0 || s.Size == 0 {
			t.Errorf("%s: value=0x%x size=%d, want both non-zero", name, s.Value, s.Size)
		}
	}

	cmd := runX86Bin(qemu, out)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42 (helper(14)=42) — the symtab must not perturb execution", code)
	}

	outNoG := filepath.Join(dir, "sym_nog.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", outNoG, p).CombinedOutput(); err != nil {
		t.Fatalf("x86-64 build: %v\n%s", err, o)
	}
	fn, err := goelf.Open(outNoG)
	if err != nil {
		t.Fatalf("open no-g ELF: %v", err)
	}
	defer fn.Close()
	if s, err := fn.Symbols(); err == nil && len(s) > 0 {
		t.Errorf("no-g binary unexpectedly has %d symbols; -g should be opt-in", len(s))
	}
}
