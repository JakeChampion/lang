package e2e

import (
	"debug/dwarf"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// machoDwarfSrc gives helper's statements lines 2 and 3 and main's call line
// 6; f carries two scalar locals for the variable DIEs, and pick's bounds
// check a numeric local label (`1:`) in the emitted text.
const machoDwarfSrc = `@noinline function helper(x: i32): i32 {
    let y: i32 = x * 2;
    return y + 1;
}
function main(): i32 {
    let a: i32 = helper(20);
    return a + f(41) + pick([1, 2, 3], 2) - 3;
}
@noinline function f(n: i32): i32 {
    let m: i32 = n + 1;
    let k: i64 = (m as i64) * 3;
    return m + (k as i32) - 168;
}
@noinline function pick(xs: i32[], i: i32): i32 {
    return xs[i];
}
`

// TestDWARFMachO: a -g arm64-darwin image carries its DWARF in a __DWARF
// segment that is not mapped, its sections flagged S_ATTR_DEBUG, which is
// where lldb and dsymutil read a static executable's debug info without a
// dSYM. The unit spans __text, each function is a subprogram inside it, the
// line table maps helper's two statements, and f's locals have DIEs. A
// default build has no __DWARF segment. Decoded host-side, so any host checks
// it; on Apple Silicon the -g image also runs.
func TestDWARFMachO(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(machoDwarfSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(out string, flags ...string) *macho.File {
		t.Helper()
		args := append(append([]string{}, flags...), "-target", "arm64-darwin", "-o", out, p)
		if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("build %v: %v\n%s", flags, err, o)
		}
		f, err := macho.Open(out)
		if err != nil {
			t.Fatalf("parse %s: %v", out, err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}

	plain := build(filepath.Join(dir, "plain"))
	if plain.Segment("__DWARF") != nil {
		t.Errorf("the default build has a __DWARF segment; -g should be required")
	}
	if _, err := plain.DWARF(); err == nil {
		t.Errorf("the default build has DWARF; -g should be required")
	}

	gpath := filepath.Join(dir, "g")
	g := build(gpath, "-g")
	seg := g.Segment("__DWARF")
	if seg == nil {
		t.Fatal("-g build has no __DWARF segment")
	}
	if seg.Memsz != 0 || seg.Maxprot != 0 || seg.Prot != 0 {
		t.Errorf("__DWARF is mapped: vmsize %#x maxprot %d initprot %d, want 0 0 0", seg.Memsz, seg.Maxprot, seg.Prot)
	}
	linkedit := g.Segment("__LINKEDIT")
	if linkedit == nil || seg.Offset+seg.Filesz > linkedit.Offset {
		t.Errorf("__DWARF [%#x,+%#x) does not end before __LINKEDIT %+v; the code signature has to come last", seg.Offset, seg.Filesz, linkedit)
	}
	for _, name := range []string{"__debug_abbrev", "__debug_info", "__debug_line", "__debug_loc"} {
		sec := g.Section(name)
		if sec == nil {
			t.Errorf("no %s section", name)
			continue
		}
		if sec.Seg != "__DWARF" || sec.Flags != 0x02000000 {
			t.Errorf("%s: segment %q flags %#x, want __DWARF and S_ATTR_DEBUG", name, sec.Seg, sec.Flags)
		}
	}

	text := g.Section("__text")
	if text == nil {
		t.Fatal("no __text section")
	}
	d, err := g.DWARF()
	if err != nil {
		t.Fatalf("DWARF(): %v", err)
	}
	r := d.Reader()
	cu, err := r.Next()
	if err != nil || cu == nil || cu.Tag != dwarf.TagCompileUnit {
		t.Fatalf("first entry is not a compile unit: %v %v", cu, err)
	}
	if lo, _ := cu.Val(dwarf.AttrLowpc).(uint64); lo != text.Addr {
		t.Errorf("CU low_pc %#x, want __text's address %#x", lo, text.Addr)
	}
	if hi, _ := cu.Val(dwarf.AttrHighpc).(uint64); hi != text.Addr+text.Size {
		t.Errorf("CU high_pc %#x, want the end of __text %#x", hi, text.Addr+text.Size)
	}
	subs := map[string][2]uint64{}
	vars := map[string]bool{}
	fn := ""
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatalf("DWARF reader: %v", err)
		}
		if e == nil {
			break
		}
		switch e.Tag {
		case dwarf.TagSubprogram:
			fn, _ = e.Val(dwarf.AttrName).(string)
			lo, _ := e.Val(dwarf.AttrLowpc).(uint64)
			hi, _ := e.Val(dwarf.AttrHighpc).(uint64)
			subs[fn] = [2]uint64{lo, hi}
		case dwarf.TagVariable, dwarf.TagFormalParameter:
			name, _ := e.Val(dwarf.AttrName).(string)
			vars[fn+"."+name] = true
		}
	}
	// An inner label is no function: as a symbol or a subprogram it would end
	// the extent of the function it sits in.
	for name := range subs {
		if strings.ContainsAny(name, ".#") {
			t.Errorf("inner label %q is a subprogram", name)
		}
	}
	for _, s := range g.Symtab.Syms {
		if strings.ContainsAny(s.Name, ".#") {
			t.Errorf("inner label %q is a symbol", s.Name)
		}
	}
	for _, name := range []string{"main", "helper", "f", "pick"} {
		pc, ok := subs[name]
		if !ok {
			t.Errorf("no subprogram DIE for %q (have %v)", name, keys(subs))
			continue
		}
		if pc[0] < text.Addr || pc[1] > text.Addr+text.Size || pc[1] <= pc[0] {
			t.Errorf("%s [%#x,%#x) is not inside __text [%#x,%#x)", name, pc[0], pc[1], text.Addr, text.Addr+text.Size)
		}
	}
	for _, v := range []string{"f.m", "f.k"} {
		if !vars[v] {
			t.Errorf("no variable DIE for %s (have %v)", v, vars)
		}
	}

	lr, err := d.LineReader(cu)
	if err != nil || lr == nil {
		t.Fatalf("LineReader: %v", err)
	}
	lines := map[int]bool{}
	var le dwarf.LineEntry
	for lr.Next(&le) == nil {
		if !le.EndSequence && le.Address >= subs["helper"][0] && le.Address < subs["helper"][1] {
			if le.File == nil || filepath.Base(le.File.Name) != "prog.fern" {
				t.Errorf("helper row at %#x names %v, want prog.fern", le.Address, le.File)
			}
			lines[le.Line] = true
		}
	}
	if !lines[2] || !lines[3] {
		t.Errorf("helper's rows cover lines %v, want its statements' lines 2 and 3", lines)
	}

	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		cmd := exec.Command(gpath)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 41 {
			t.Errorf("the -g image exits %d, want 41", code)
		}
	}
}

// TestLLDBBreakpointByLine is what -g is for on arm64-darwin: lldb, reading
// the executable's own __DWARF segment, sets a breakpoint by file:line, stops
// there, and prints a backtrace naming each Fern function and its line, and a
// local's value. Skips only where lldb or Apple Silicon is absent.
func TestLLDBBreakpointByLine(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("lldb runs an arm64-darwin image only on Apple Silicon")
	}
	lldb, err := exec.LookPath("lldb")
	if err != nil {
		t.Skip("lldb not on PATH")
	}
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(machoDwarfSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	if o, err := exec.Command(bin, "-g", "-target", "arm64-darwin", "-o", out, p).CombinedOutput(); err != nil {
		t.Fatalf("-g build: %v\n%s", err, o)
	}
	o, err := exec.Command(lldb, "--batch", "-o", "breakpoint set --file prog.fern --line 3", "-o", "run", "-o", "bt", "-o", "frame variable y", "--", out).CombinedOutput()
	got := string(o)
	if err != nil {
		t.Fatalf("lldb: %v\n%s", err, got)
	}
	for _, want := range []string{
		"where = prog`helper",
		"stop reason = breakpoint 1.1",
		"`helper(",
		"at prog.fern:3:",
		"`main at prog.fern:6:",
		"y = 40",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lldb output lacks %q\n%s", want, got)
		}
	}
}
