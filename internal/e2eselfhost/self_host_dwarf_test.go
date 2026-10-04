package e2eselfhost

import (
	"debug/dwarf"
	goelf "debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// rowsIn is the line-table rows inside a function's code.
func rowsIn(t *testing.T, subs map[string][2]uint64, rows []dwarf.LineEntry, name string) []dwarf.LineEntry {
	t.Helper()
	pc, ok := subs[name]
	if !ok {
		t.Fatalf("no subprogram %q (have %v)", name, subs)
	}
	var out []dwarf.LineEntry
	for _, le := range rows {
		if le.Address >= pc[0] && le.Address < pc[1] {
			out = append(out, le)
		}
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
			for off := 0; off < len(df); {
				if off+8 > len(df) {
					t.Fatalf(".debug_frame ends inside an entry header at %#x", off)
				}
				n := int(binary.LittleEndian.Uint32(df[off:]))
				if n == 0 {
					t.Fatalf(".debug_frame has a zero-length entry at %#x; that terminator belongs to .eh_frame", off)
				}
				if off+4+n > len(df) || (binary.LittleEndian.Uint32(df[off+4:]) != 0xffffffff && n < 20) {
					t.Fatalf(".debug_frame entry at %#x of length %d does not fit the section (%#x bytes)", off, n, len(df))
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

// dwarfVarsSrc gives the variables a debugger has to find in every kind of
// home: f's in registers (and its string parameter, which is described by no
// DIE), sum's carried round a loop, and many's, which outnumber the
// registers across the calls between them and so live in frame slots.
const dwarfVarsSrc = `@noinline function bump(x: i64): i64 { return x + 1; }
@noinline function f(s: string, n: i32): i32 {
    let m: i32 = n + 1;
    let k: i64 = (m as i64) * 3;
    return m + s.len() + (k as i32);
}
@noinline function sum(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        acc = acc + i;
        i = i + 1;
    }
    return acc;
}
@noinline function many(a: i64): i64 {
    let b: i64 = bump(a);
    let c: i64 = bump(b) + a;
    let d: i64 = bump(c) + b;
    let e: i64 = bump(d) + c + a;
    let g: i64 = bump(e) + d + b + a;
    let h: i64 = bump(g) + e + c + b + a;
    let j: i64 = bump(h) + g + d + c + b + a;
    let k: i64 = bump(j) + h + g + e + d + c + b + a;
    let l: i64 = bump(k) + j + h + g + e + d + c + b + a;
    let o: i64 = bump(l) + k + j + h + g + e + d + c + b + a;
    let p: i64 = bump(o) + l + k + j + h + g + e + d + c + b + a;
    let q: i64 = bump(p) + o + l + k + j + h + g + e + d + c + b + a;
    return q + p + o + l + k + j + h + g + e + d + c + b + a;
}
function main(): i32 {
    return f("hi", 41) + sum(10) + (many(1) % 100) as i32;
}
`

// dwarfVar is one variable DIE of a subprogram: its tag, type name, and
// location-list entries as absolute [lo, hi) ranges with their expressions.
type dwarfVar struct {
	tag    dwarf.Tag
	typ    string
	ranges [][2]uint64
	exprs  [][]byte
}

// dwarfVars reads each subprogram's variable children, decoding their
// location lists from .debug_loc against the unit's low_pc.
func dwarfVars(t *testing.T, path string) (map[string]map[string]dwarfVar, map[string][2]uint64) {
	t.Helper()
	f, err := goelf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	var loc []byte
	if sec := f.Section(".debug_loc"); sec != nil {
		if loc, err = sec.Data(); err != nil {
			t.Fatal(err)
		}
	}
	out := map[string]map[string]dwarfVar{}
	subs := map[string][2]uint64{}
	r := d.Reader()
	var base uint64
	fn := ""
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			break
		}
		switch e.Tag {
		case dwarf.TagCompileUnit:
			base, _ = e.Val(dwarf.AttrLowpc).(uint64)
		case dwarf.TagSubprogram:
			fn, _ = e.Val(dwarf.AttrName).(string)
			lo, _ := e.Val(dwarf.AttrLowpc).(uint64)
			hi, _ := e.Val(dwarf.AttrHighpc).(uint64)
			subs[fn] = [2]uint64{lo, hi}
		case dwarf.TagFormalParameter, dwarf.TagVariable:
			name, _ := e.Val(dwarf.AttrName).(string)
			v := dwarfVar{tag: e.Tag}
			if off, ok := e.Val(dwarf.AttrType).(dwarf.Offset); ok {
				if ty, err := d.Type(off); err == nil {
					v.typ = ty.String()
				}
			}
			at, ok := e.Val(dwarf.AttrLocation).(int64)
			if !ok {
				t.Errorf("%s.%s: DW_AT_location is %T, want a location-list offset", fn, name, e.Val(dwarf.AttrLocation))
			}
			for p := int(at); ok && p+16 <= len(loc); {
				lo := binary.LittleEndian.Uint64(loc[p:])
				hi := binary.LittleEndian.Uint64(loc[p+8:])
				if lo == 0 && hi == 0 {
					break
				}
				n := int(binary.LittleEndian.Uint16(loc[p+16:]))
				v.ranges = append(v.ranges, [2]uint64{base + lo, base + hi})
				v.exprs = append(v.exprs, loc[p+18:p+18+n])
				p += 18 + n
			}
			if out[fn] == nil {
				out[fn] = map[string]dwarfVar{}
			}
			out[fn][name] = v
		}
	}
	return out, subs
}

// The self-host's answer to internal/e2e's TestDWARFLocalVars (#11410). A
// variable's value moves between homes the register allocator picks, so its
// DW_AT_location is a location list: each entry a range of f's code and the
// register (DW_OP_reg<n>) or frame slot (DW_OP_breg<fp>) the value is in
// there. Native's fixed DW_OP_fbreg offsets do not exist here, so this checks
// the shape on both ISAs, and on an x86-64 host with gdb what the debugger
// reads at a breakpoint.
func TestSelfHostDWARFLocalVars(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "vars.fern")
	if err := os.WriteFile(src, []byte(dwarfVarsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	wants := map[string]map[string]string{
		"f":    {"n": "i32", "m": "i32", "k": "i64"},
		"sum":  {"n": "i32", "acc": "i32", "i": "i32"},
		"many": {"a": "i64", "b": "i64", "c": "i64", "d": "i64", "e": "i64", "g": "i64", "h": "i64", "j": "i64", "k": "i64", "l": "i64", "o": "i64", "p": "i64", "q": "i64"},
	}
	params := map[string]bool{"n": true, "a": true}
	fp := map[string]byte{"x86-64-linux": 0x76, "arm64-linux": 0x8d}
	bins := map[string]string{}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "vars-"+target)
			cmd := runX86_64Bin(cli.runner, cli.bin, "-g", "-target", target, "-o", out, src, cli.stdlib)
			if o, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			bins[target] = out
			vars, subs := dwarfVars(t, out)
			if _, ok := vars["f"]["s"]; ok {
				t.Errorf("f's string parameter s has a DIE; only scalars are described")
			}
			spilled := false
			for fn, want := range wants {
				for name, typ := range want {
					v, ok := vars[fn][name]
					if !ok {
						t.Errorf("%s: no DIE for %s (have %v)", fn, name, vars[fn])
						continue
					}
					if wantTag := map[bool]dwarf.Tag{true: dwarf.TagFormalParameter, false: dwarf.TagVariable}[params[name]]; v.tag != wantTag {
						t.Errorf("%s.%s: tag %v, want %v", fn, name, v.tag, wantTag)
					}
					if v.typ != typ {
						t.Errorf("%s.%s: type %q, want %q", fn, name, v.typ, typ)
					}
					if len(v.ranges) == 0 {
						t.Errorf("%s.%s: an empty location list", fn, name)
					}
					for i, rg := range v.ranges {
						if rg[0] >= rg[1] || rg[0] < subs[fn][0] || rg[1] > subs[fn][1] {
							t.Errorf("%s.%s: range [%#x,%#x) is not inside %s [%#x,%#x)", fn, name, rg[0], rg[1], fn, subs[fn][0], subs[fn][1])
						}
						e := v.exprs[i]
						switch {
						case len(e) == 1 && e[0] >= 0x50 && e[0] <= 0x6f:
						case len(e) >= 2 && e[0] == fp[target]:
							spilled = true
						default:
							t.Errorf("%s.%s: location % x is neither DW_OP_reg<n> nor the frame register's DW_OP_breg", fn, name, e)
						}
					}
				}
			}
			if !spilled {
				t.Errorf("no variable of many is in a frame slot; the case no longer exercises a spilled home")
			}
		})
	}

	// What a debugger reads. gdb is the consumer -g serves; where it is
	// absent the shape checks above still stand.
	gdb, err := exec.LookPath("gdb")
	if err != nil || runtime.GOARCH != "amd64" || bins["x86-64-linux"] == "" {
		t.Skip("gdb on an amd64 host reads the x86-64 binary's variables; none here")
	}
	o, err := exec.Command(gdb, "-batch", "-ex", "break vars.fern:5", "-ex", "break vars.fern:12", "-ex", "break vars.fern:29",
		"-ex", "run", "-ex", "info locals", "-ex", "continue", "-ex", "info locals", "-ex", "delete 2", "-ex", "continue", "-ex", "info args", "-ex", "info locals",
		bins["x86-64-linux"]).CombinedOutput()
	if err != nil {
		t.Fatalf("gdb: %v\n%s", err, o)
	}
	got := string(o)
	for _, want := range []string{
		"m = 42\nk = 126\n", // f at its return
		"acc = 0\ni = 0\n",  // sum's first iteration
		"a = 1\n",           // many's parameter, at its return
		"b = 2\nc = 4\nd = 7\ne = 13\ng = 24\nh = 45\nj = 84\nk = 181\nl = 362\no = 724\np = 1448\nq = 2896\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("gdb did not read %q\n%s", want, got)
		}
	}
}

const dwarfStructsSrc = `struct Point { x: i32, y: i32 }
struct Person { name: string, age: i32, score: i32 }
struct Rect { origin: Point, w: i32, h: i32 }
@noinline function describe(p: Person): i32 { return p.age + p.score; }
@noinline function area(r: Rect): i32 { return r.w * r.h; }
function main(): i32 {
    let pt: Point = Point { x: 7, y: 35 };
    let p: Person = Person { name: "Ada", age: 36, score: 99 };
    let r: Rect = Rect { origin: Point { x: 3, y: 4 }, w: 5, h: 6 };
    return pt.x + pt.y + describe(p) + area(r) + r.origin.x;
}
`

// dwarfVarStruct is the struct a pointer-typed variable of fn points at.
func dwarfVarStruct(t *testing.T, path, fn, name string) *dwarf.StructType {
	t.Helper()
	f, err := goelf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	r := d.Reader()
	cur := ""
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			break
		}
		if e.Tag == dwarf.TagSubprogram {
			cur, _ = e.Val(dwarf.AttrName).(string)
			continue
		}
		if n, _ := e.Val(dwarf.AttrName).(string); cur != fn || n != name || (e.Tag != dwarf.TagVariable && e.Tag != dwarf.TagFormalParameter) {
			continue
		}
		off, ok := e.Val(dwarf.AttrType).(dwarf.Offset)
		if !ok {
			t.Fatalf("%s.%s has no DW_AT_type", fn, name)
		}
		ty, err := d.Type(off)
		if err != nil {
			t.Fatal(err)
		}
		ptr, ok := ty.(*dwarf.PtrType)
		if !ok {
			t.Fatalf("%s.%s is a %T (%v), want a pointer to its box", fn, name, ty, ty)
		}
		st, ok := ptr.Type.(*dwarf.StructType)
		if !ok {
			t.Fatalf("%s.%s points to a %T, want a struct", fn, name, ptr.Type)
		}
		return st
	}
	t.Fatalf("no DIE for %s.%s", fn, name)
	return nil
}

// checkDwarfStruct holds a struct DIE to its name, size and members, each
// member given as name, offset and type.
func checkDwarfStruct(t *testing.T, st *dwarf.StructType, name string, size int64, members [][3]string) {
	t.Helper()
	if st.StructName != name || st.ByteSize != size {
		t.Errorf("struct %s of %d bytes, want %s of %d", st.StructName, st.ByteSize, name, size)
	}
	if len(st.Field) != len(members) {
		t.Fatalf("%s has members %v, want %v", name, st.Field, members)
	}
	for i, m := range members {
		f := st.Field[i]
		if got := [3]string{f.Name, strconv.FormatInt(f.ByteOffset, 10), f.Type.String()}; got != m {
			t.Errorf("%s member %d = %v, want %v", name, i, got, m)
		}
	}
}

// The self-host's answer to internal/e2e's TestDWARFStructVars,
// TestDWARFMixedStructVars and TestDWARFNestedStructVars (#11410). A struct
// variable holds a pointer to its box, whose word 0 is the shape and whose
// field i is the 8-byte slot at (i+1)*8, so its type is a pointer to a struct
// of that layout. A string field is left out and the fields after it keep
// their offsets; a struct field is a pointer to its own box.
func TestSelfHostDWARFStructVars(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "structs.fern")
	if err := os.WriteFile(src, []byte(dwarfStructsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	point := [][3]string{{"x", "8", "i32"}, {"y", "16", "i32"}}
	bins := map[string]string{}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "structs-"+target)
			cmd := runX86_64Bin(cli.runner, cli.bin, "-g", "-target", target, "-o", out, src, cli.stdlib)
			if o, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			bins[target] = out
			checkDwarfStruct(t, dwarfVarStruct(t, out, "main", "pt"), "Point", 24, point)
			checkDwarfStruct(t, dwarfVarStruct(t, out, "describe", "p"), "Person", 32, [][3]string{{"age", "16", "i32"}, {"score", "24", "i32"}})
			rect := dwarfVarStruct(t, out, "area", "r")
			checkDwarfStruct(t, rect, "Rect", 32, [][3]string{{"origin", "8", "*struct Point"}, {"w", "16", "i32"}, {"h", "24", "i32"}})
			if p, ok := rect.Field[0].Type.(*dwarf.PtrType); ok {
				if st, ok := p.Type.(*dwarf.StructType); ok {
					checkDwarfStruct(t, st, "Point", 24, point)
				}
			}
		})
	}

	gdb, err := exec.LookPath("gdb")
	if err != nil || runtime.GOARCH != "amd64" || bins["x86-64-linux"] == "" {
		t.Skip("gdb on an amd64 host reads the x86-64 binary's structs; none here")
	}
	o, err := exec.Command(gdb, "-batch", "-ex", "break area", "-ex", "run", "-ex", "print *r", "-ex", "print *r.origin", bins["x86-64-linux"]).CombinedOutput()
	if err != nil {
		t.Fatalf("gdb: %v\n%s", err, o)
	}
	for _, want := range []string{"w = 5, h = 6}", "= {x = 3, y = 4}"} {
		if !strings.Contains(string(o), want) {
			t.Errorf("gdb did not read %q\n%s", want, o)
		}
	}
}
