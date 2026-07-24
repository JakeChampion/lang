package e2e

import (
	"debug/dwarf"
	goelf "debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDWARFDebugInfo is the end-to-end guard for the -g DWARF debug info
// (#5537 slice 3): a real `fern -g` build emits a .debug_info compilation unit
// whose subprogram DIEs name every emitted function with a PC range inside the
// CU's [low_pc, high_pc) — decodable through Go's debug/dwarf, which is the
// same information gdb/lldb use to break by name and unwind frames. Without
// -g there is no DWARF at all (default binaries stay small).
func TestDWARFDebugInfo(t *testing.T) {
	src := `function helper(x: i32): i32 { return x * 2; }
function main(): i32 { return helper(21); }
`
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// Default build: no DWARF.
	plain := filepath.Join(dir, "plain.bin")
	if o, err := exec.Command(bin, "-target", "x86-64", "-o", plain, p).CombinedOutput(); err != nil {
		t.Fatalf("plain build: %v\n%s", err, o)
	}
	if f, err := goelf.Open(plain); err == nil {
		if _, derr := f.DWARF(); derr == nil {
			t.Errorf("default build has DWARF; -g should be required")
		}
		f.Close()
	}

	// -g build: DWARF present.
	out := filepath.Join(dir, "g.bin")
	if o, err := exec.Command(bin, "-g", "-target", "x86-64", "-o", out, p).CombinedOutput(); err != nil {
		t.Fatalf("-g build: %v\n%s", err, o)
	}
	f, err := goelf.Open(out)
	if err != nil {
		t.Fatalf("open ELF: %v", err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatalf("DWARF() on -g build: %v", err)
	}

	r := d.Reader()
	var cuLo, cuHi uint64
	subs := map[string][2]uint64{}
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatalf("DWARF reader: %v", err)
		}
		if e == nil {
			break
		}
		switch e.Tag {
		case dwarf.TagCompileUnit:
			cuLo, _ = e.Val(dwarf.AttrLowpc).(uint64)
			cuHi, _ = e.Val(dwarf.AttrHighpc).(uint64)
			if prod, _ := e.Val(dwarf.AttrProducer).(string); prod != "fern" {
				t.Errorf("CU producer = %q, want %q", prod, "fern")
			}
		case dwarf.TagSubprogram:
			name, _ := e.Val(dwarf.AttrName).(string)
			lo, _ := e.Val(dwarf.AttrLowpc).(uint64)
			hi, _ := e.Val(dwarf.AttrHighpc).(uint64)
			subs[name] = [2]uint64{lo, hi}
		}
	}
	if cuHi <= cuLo {
		t.Fatalf("CU pc range empty: [%#x,%#x)", cuLo, cuHi)
	}
	// Both user functions must have a subprogram DIE whose range sits inside
	// the CU range and is non-empty.
	for _, name := range []string{"main", "helper"} {
		pc, ok := subs[name]
		if !ok {
			t.Errorf("missing subprogram DIE for %q (have %v)", name, keys(subs))
			continue
		}
		if pc[0] < cuLo || pc[1] > cuHi || pc[1] <= pc[0] {
			t.Errorf("%q pc range [%#x,%#x) not within CU [%#x,%#x)", name, pc[0], pc[1], cuLo, cuHi)
		}
	}
}

func keys(m map[string][2]uint64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
