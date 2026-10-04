package e2eselfhost

import (
	"debug/dwarf"
	goelf "debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The self-host twin of internal/e2e's TestDWARFDebugInfo (#11410): a `-g`
// image carries a DWARF compilation unit a debugger decodes, with a
// subprogram per function named as the source names it and spanning its
// code, and the default build carries no DWARF. Decoded host-side through
// Go's debug/dwarf, so both ELF targets are checked without running.
func TestSelfHostDWARFDebugInfo(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("@noinline function helper(x: i32): i32 { return x * 2; }\nfunction main(): i32 { return helper(21); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(t *testing.T, out string, args ...string) {
		t.Helper()
		args = append(args, "-o", out, src, cli.stdlib)
		if o, err := runX86_64Bin(cli.runner, cli.bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("fern %v: %v\n%s", args, err, o)
		}
	}

	plain := filepath.Join(dir, "plain.bin")
	build(t, plain, "-target", "x86-64-linux")
	if f, err := goelf.Open(plain); err == nil {
		if _, derr := f.DWARF(); derr == nil {
			t.Errorf("the default build has DWARF; -g should be required")
		}
		f.Close()
	}

	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			build(t, out, "-g", "-target", target)
			f, err := goelf.Open(out)
			if err != nil {
				t.Fatalf("open ELF: %v", err)
			}
			defer f.Close()
			d, err := f.DWARF()
			if err != nil {
				t.Fatalf("DWARF() on the -g build: %v", err)
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
						t.Errorf("CU producer = %q, want \"fern\"", prod)
					}
					if nm, _ := e.Val(dwarf.AttrName).(string); nm != src {
						t.Errorf("CU name = %q, want the entry path %q", nm, src)
					}
					if cd, _ := e.Val(dwarf.AttrCompDir).(string); cd == "" {
						t.Errorf("CU comp_dir is empty; want the compile directory")
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
			// The user functions carry source names, not the `__fn_` symbols,
			// and each range sits inside the CU's and is non-empty.
			for _, name := range []string{"main", "helper"} {
				pc, ok := subs[name]
				if !ok {
					t.Errorf("no subprogram DIE for %q (have %d: %v)", name, len(subs), subs)
					continue
				}
				if pc[0] < cuLo || pc[1] > cuHi || pc[1] <= pc[0] {
					t.Errorf("%q pc range [%#x,%#x) not within the CU [%#x,%#x)", name, pc[0], pc[1], cuLo, cuHi)
				}
			}
			if _, ok := subs["__fn_main"]; ok {
				t.Errorf("a subprogram is named by its symbol, __fn_main, rather than its source name")
			}
		})
	}
}

// dwarfSubprograms decodes a -g image's subprogram PC ranges by source name
// and its line-table rows (end-of-sequence rows left out).
func dwarfSubprograms(t *testing.T, out string) (map[string][2]uint64, []dwarf.LineEntry) {
	t.Helper()
	f, err := goelf.Open(out)
	if err != nil {
		t.Fatalf("open ELF: %v", err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatalf("DWARF(): %v", err)
	}
	r := d.Reader()
	cu, err := r.Next()
	if err != nil || cu == nil {
		t.Fatalf("no CU: %v", err)
	}
	subs := map[string][2]uint64{}
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatalf("DWARF reader: %v", err)
		}
		if e == nil {
			break
		}
		if e.Tag == dwarf.TagSubprogram {
			name, _ := e.Val(dwarf.AttrName).(string)
			lo, _ := e.Val(dwarf.AttrLowpc).(uint64)
			hi, _ := e.Val(dwarf.AttrHighpc).(uint64)
			subs[name] = [2]uint64{lo, hi}
		}
	}
	lr, err := d.LineReader(cu)
	if err != nil {
		t.Fatalf("LineReader (the -g image should carry .debug_line): %v", err)
	}
	var rows []dwarf.LineEntry
	for {
		var le dwarf.LineEntry
		if err := lr.Next(&le); err != nil {
			break
		}
		if !le.EndSequence {
			rows = append(rows, le)
		}
	}
	return subs, rows
}

// rowsIn is the line-table rows inside a function's code. The self-host
// emits a function as its register-ABI body, `name.r`, behind a stack-ABI
// entry, `name`, so the rows of one source function sit in either range.
func rowsIn(t *testing.T, subs map[string][2]uint64, rows []dwarf.LineEntry, name string) []dwarf.LineEntry {
	t.Helper()
	var out []dwarf.LineEntry
	found := false
	for _, n := range []string{name, name + ".r"} {
		pc, ok := subs[n]
		if !ok {
			continue
		}
		found = true
		for _, le := range rows {
			if le.Address >= pc[0] && le.Address < pc[1] {
				out = append(out, le)
			}
		}
	}
	if !found {
		t.Fatalf("no subprogram %q (have %v)", name, subs)
	}
	return out
}

// The self-host twin of internal/e2e's TestDWARFLineTable (#11410): under -g
// the ELF image carries a .debug_line table with a row per statement, so a
// function whose body spans several lines maps each of them, and the default
// build carries no line table.
func TestSelfHostDWARFLineTable(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	// helper: decl line 1, `let y` line 2, `return` line 3.
	// main:   decl line 5, `let a` line 6, `return` line 7.
	src := filepath.Join(dir, "prog.fern")
	prog := "@noinline function helper(x: i32): i32 {\n    let y: i32 = x * 2;\n    return y + 1;\n}\nfunction main(): i32 {\n    let a: i32 = helper(20);\n    return a;\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(t *testing.T, out string, args ...string) {
		t.Helper()
		args = append(args, "-o", out, src, cli.stdlib)
		if o, err := runX86_64Bin(cli.runner, cli.bin, args...).CombinedOutput(); err != nil {
			t.Fatalf("fern %v: %v\n%s", args, err, o)
		}
	}

	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			build(t, out, "-g", "-target", target)
			subs, rows := dwarfSubprograms(t, out)
			spans := map[string][2]int{"helper": {1, 3}, "main": {5, 7}}
			for name, span := range spans {
				seen := map[int]bool{}
				for _, le := range rowsIn(t, subs, rows, name) {
					if le.Line >= span[0] && le.Line <= span[1] {
						seen[le.Line] = true
					}
				}
				if len(seen) == 0 {
					t.Errorf("%s: no line-table row with a line in %v (rows: %v)", name, span, rows)
				}
				if name == "helper" && len(seen) < 2 {
					t.Errorf("helper: want rows for at least two distinct lines (one per statement), got %v", seen)
				}
			}
		})
	}

	plain := filepath.Join(dir, "plain.bin")
	build(t, plain, "-target", "x86-64-linux")
	if f, err := goelf.Open(plain); err == nil {
		if d, derr := f.DWARF(); derr == nil {
			if cu, _ := d.Reader().Next(); cu != nil {
				if _, lerr := d.LineReader(cu); lerr == nil {
					t.Errorf("the default build has a .debug_line table; -g should be required")
				}
			}
		}
		f.Close()
	}
}

// The self-host twin of internal/e2e's TestDWARFMultiFile (#11410): the rows
// of an imported module's function name that module's file, every row has a
// column, exactly a function's first row is prologue_end, addr2line resolves
// a row's address to its file and line, and .debug_frame has an FDE over
// each function.
func TestSelfHostDWARFMultiFile(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	lib := "@noinline pub function twice(x: i32): i32 {\n    let y: i32 = x * 2;\n    return y;\n}\n"
	main := "import \"./lib/util\";\nfunction main(): i32 {\n    let a: i32 = util.twice(20);\n    return a + 2;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "lib", "util.fern"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	addr2line, _ := exec.LookPath("addr2line")

	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "prog-"+target)
			cmd := runX86_64Bin(cli.runner, cli.bin, "-g", "-target", target, "-o", out, "main.fern", cli.stdlib)
			cmd.Dir = dir
			if o, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			subs, rows := dwarfSubprograms(t, out)
			want := map[string]string{"util__twice": filepath.Join("lib", "util.fern"), "main": "main.fern"}
			for fn, file := range want {
				inFn := rowsIn(t, subs, rows, fn)
				if len(inFn) < 2 {
					t.Errorf("%s: %d line rows, want one per statement", fn, len(inFn))
					continue
				}
				for i, le := range inFn {
					if le.File == nil || !strings.HasSuffix(le.File.Name, file) {
						t.Errorf("%s row %d at %#x names %v, want a path ending in %s", fn, i, le.Address, le.File, file)
					}
					if le.Column == 0 {
						t.Errorf("%s row %d at %#x has no column", fn, i, le.Address)
					}
					if le.PrologueEnd != (i == 0) {
						t.Errorf("%s row %d at %#x prologue_end=%v; exactly the first row should carry it", fn, i, le.Address, le.PrologueEnd)
					}
					if !le.IsStmt {
						t.Errorf("%s row %d at %#x is not is_stmt", fn, i, le.Address)
					}
				}
				if addr2line != "" {
					o, err := exec.Command(addr2line, "-e", out, fmt.Sprintf("%#x", inFn[0].Address)).Output()
					if err != nil {
						t.Fatal(err)
					}
					if got := strings.TrimSpace(string(o)); !strings.HasSuffix(got, file+":"+strconv.Itoa(inFn[0].Line)) {
						t.Errorf("addr2line %#x = %q, want a path ending in %s:%d", inFn[0].Address, got, file, inFn[0].Line)
					}
				}
			}

			// .debug_frame: the debugger-facing container of the unwind
			// rules, one FDE per function at its exact range.
			f, err := goelf.Open(out)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			// An inner label (a register-ABI entry `f.r`) is no symbol of
			// its own, or the function's extent would end at it.
			syms, err := f.Symbols()
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range syms {
				if strings.Contains(s.Name, ".") {
					t.Errorf("inner label %q is a .symtab symbol", s.Name)
				}
			}
			sec := f.Section(".debug_frame")
			if sec == nil {
				t.Fatal("no .debug_frame in the -g image")
			}
			df, err := sec.Data()
			if err != nil {
				t.Fatal(err)
			}
			covered := map[uint64]uint64{}
			for off := 0; off+8 <= len(df); {
				n := int(binary.LittleEndian.Uint32(df[off:]))
				if n == 0 {
					t.Fatalf(".debug_frame has a zero-length entry at %#x; that terminator belongs to .eh_frame", off)
				}
				if (off+4+n)%8 != 0 {
					t.Errorf(".debug_frame entry at %#x ends at %#x, not on 8", off, off+4+n)
				}
				if binary.LittleEndian.Uint32(df[off+4:]) != 0xffffffff {
					lo := binary.LittleEndian.Uint64(df[off+8:])
					covered[lo] = lo + binary.LittleEndian.Uint64(df[off+16:])
				}
				off += 4 + n
			}
			for fn := range want {
				pc := subs[fn]
				if hi, ok := covered[pc[0]]; !ok || hi != pc[1] {
					t.Errorf("%s [%#x,%#x): .debug_frame FDE covers [%#x,%#x)", fn, pc[0], pc[1], pc[0], hi)
				}
			}
		})
	}
}
