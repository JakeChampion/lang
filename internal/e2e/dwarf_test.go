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
			// DW_AT_name + DW_AT_comp_dir must be set so a debugger can locate
			// and display the source (not just addresses).
			if nm, _ := e.Val(dwarf.AttrName).(string); nm == "" {
				t.Errorf("CU DW_AT_name is empty; want the source path")
			}
			if cd, _ := e.Val(dwarf.AttrCompDir).(string); cd == "" {
				t.Errorf("CU DW_AT_comp_dir is empty; want the compile directory")
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

// TestDWARFLineTable is the end-to-end guard for the -g .debug_line
// per-statement source-line table (#5537 slice 2): a real `fern -g` build maps
// code addresses to their exact source lines, decodable through Go's
// debug/dwarf LineReader — the information gdb/lldb and addr2line use to step
// by source line and turn a backtrace address into `file:line`. Both native
// backends (x86-64 and arm64) emit `.loc` per statement, so a multi-statement
// function's PC range carries a row for each of its lines. The default build
// has no line table.
func TestDWARFLineTable(t *testing.T) {
	// helper: decl line 1, `var y` line 2, `return` line 3.
	// main:   decl line 5, `var a` line 6, `return` line 7.
	src := "function helper(x: i32): i32 {\n    var y: i32 = x * 2;\n    return y + 1;\n}\nfunction main(): i32 {\n    var a: i32 = helper(20);\n    return a;\n}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// The line table is target-independent (address_size 8 both ways) and
	// decoded host-side, so we build for each target and parse without running.
	for _, target := range []string{"x86-64", "arm64"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			if o, err := exec.Command(bin, "-g", "-target", target, "-o", out, p).CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			f, err := goelf.Open(out)
			if err != nil {
				t.Fatalf("open ELF: %v", err)
			}
			defer f.Close()
			d, err := f.DWARF()
			if err != nil {
				t.Fatalf("DWARF(): %v", err)
			}

			// Subprogram PC ranges per function.
			r := d.Reader()
			cu, err := r.Next()
			if err != nil || cu == nil {
				t.Fatalf("no CU: %v", err)
			}
			type pcRange struct{ lo, hi uint64 }
			ranges := map[string]pcRange{}
			for {
				e, err := r.Next()
				if err != nil {
					t.Fatalf("reader: %v", err)
				}
				if e == nil {
					break
				}
				if e.Tag == dwarf.TagSubprogram {
					name, _ := e.Val(dwarf.AttrName).(string)
					lo, _ := e.Val(dwarf.AttrLowpc).(uint64)
					hi, _ := e.Val(dwarf.AttrHighpc).(uint64)
					ranges[name] = pcRange{lo, hi}
				}
			}

			lr, err := d.LineReader(cu)
			if err != nil {
				t.Fatalf("LineReader (expected .debug_line under -g): %v", err)
			}
			type row struct {
				addr uint64
				line int
			}
			var rows []row
			var le dwarf.LineEntry
			for {
				if err := lr.Next(&le); err != nil {
					break
				}
				if !le.EndSequence {
					rows = append(rows, row{le.Address, le.Line})
				}
			}
			// Per-statement granularity: `helper` has two body statements on
			// distinct lines (2 and 3), so its PC range must carry rows for at
			// least two distinct source lines within its 1-3 span — a
			// per-function table (one decl-line row) would fail this. `main`
			// (lines 5-7) must likewise map somewhere in its span.
			spans := map[string][2]int{"helper": {1, 3}, "main": {5, 7}}
			for name, span := range spans {
				pc, ok := ranges[name]
				if !ok {
					t.Errorf("no subprogram %q", name)
					continue
				}
				seen := map[int]bool{}
				for _, rw := range rows {
					if rw.addr >= pc.lo && rw.addr < pc.hi && rw.line >= span[0] && rw.line <= span[1] {
						seen[rw.line] = true
					}
				}
				if len(seen) == 0 {
					t.Errorf("%s [%#x,%#x): no line-table row with line in %v (rows: %v)", name, pc.lo, pc.hi, span, rows)
				}
				if name == "helper" && len(seen) < 2 {
					t.Errorf("helper [%#x,%#x): want >=2 distinct source lines (per-statement), got %v (rows: %v)", pc.lo, pc.hi, seen, rows)
				}
			}
		})
	}

	// Default build: no line table.
	plain := filepath.Join(dir, "plain.bin")
	if o, err := exec.Command(bin, "-target", "x86-64", "-o", plain, p).CombinedOutput(); err != nil {
		t.Fatalf("plain build: %v\n%s", err, o)
	}
	if pf, err := goelf.Open(plain); err == nil {
		if pd, derr := pf.DWARF(); derr == nil {
			if pr := pd.Reader(); pr != nil {
				if pcu, _ := pr.Next(); pcu != nil {
					if _, lerr := pd.LineReader(pcu); lerr == nil {
						t.Errorf("default build has a .debug_line table; -g should be required")
					}
				}
			}
		}
		pf.Close()
	}
}

func keys(m map[string][2]uint64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDWARFLocalVars is the end-to-end guard for the -g DWARF variable DIEs
// (#5537 slice 3 locals/params): a real `fern -g` x86-64 build gives each
// function's scalar parameters and locals a DIE with a name, an i32/f64/…
// base type, and a frame-relative location — what gdb/lldb use for `info args`
// / `info locals` / `print <var>`. (Verified live: gdb prints a=20, b=22,
// sum=42 at a source-line breakpoint.)
func TestDWARFLocalVars(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 {\n    var sum: i32 = a + b;\n    return sum;\n}\nfunction main(): i32 {\n    return add(20, 22);\n}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
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
		t.Fatalf("DWARF(): %v", err)
	}

	// Find the `add` subprogram and collect its variable children.
	r := d.Reader()
	vars := map[string]struct {
		tag  dwarf.Tag
		typ  string
		size int64
		loc  bool
	}{}
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatalf("reader: %v", err)
		}
		if e == nil {
			break
		}
		if e.Tag != dwarf.TagSubprogram {
			continue
		}
		if name, _ := e.Val(dwarf.AttrName).(string); name != "add" {
			continue
		}
		for {
			c, err := r.Next()
			if err != nil {
				t.Fatalf("reader: %v", err)
			}
			if c == nil || c.Tag == 0 {
				break
			}
			name, _ := c.Val(dwarf.AttrName).(string)
			v := struct {
				tag  dwarf.Tag
				typ  string
				size int64
				loc  bool
			}{tag: c.Tag}
			if toff, ok := c.Val(dwarf.AttrType).(dwarf.Offset); ok {
				if typ, terr := d.Type(toff); terr == nil {
					v.typ, v.size = typ.String(), typ.Size()
				}
			}
			_, v.loc = c.Val(dwarf.AttrLocation).([]byte)
			vars[name] = v
		}
		break
	}

	for name, want := range map[string]struct {
		tag dwarf.Tag
	}{
		"a":   {dwarf.TagFormalParameter},
		"b":   {dwarf.TagFormalParameter},
		"sum": {dwarf.TagVariable},
	} {
		v, ok := vars[name]
		if !ok {
			t.Errorf("add: missing variable DIE %q (have %v)", name, vars)
			continue
		}
		if v.tag != want.tag {
			t.Errorf("%s: tag = %v, want %v", name, v.tag, want.tag)
		}
		if v.typ != "i32" || v.size != 4 {
			t.Errorf("%s: type = %q/%d, want i32/4", name, v.typ, v.size)
		}
		if !v.loc {
			t.Errorf("%s: missing DW_AT_location", name)
		}
	}
}
