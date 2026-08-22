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
	// @noinline keeps the probe a real function: this case asserts what the
	// subprogram DIEs NAME, and ir.Inline would substitute helper into its sole
	// call site with the dead-function cull then removing it.
	src := `@noinline function helper(x: i32): i32 { return x * 2; }
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
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", plain, p).CombinedOutput(); err != nil {
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
	if o, err := exec.Command(bin, "-g", "-target", "x86-64-linux", "-o", out, p).CombinedOutput(); err != nil {
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
	// @noinline on line 1 (so the line numbers above still hold) keeps helper a
	// real function with its own PC range to carry line rows.
	src := "@noinline function helper(x: i32): i32 {\n    var y: i32 = x * 2;\n    return y + 1;\n}\nfunction main(): i32 {\n    var a: i32 = helper(20);\n    return a;\n}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// The line table is target-independent (address_size 8 both ways) and
	// decoded host-side, so we build for each target and parse without running.
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
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
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", plain, p).CombinedOutput(); err != nil {
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
	// `s` (string) precedes the scalars, so the two backends' frame layouts
	// diverge: x86-64's single-word string makes every slot 8 bytes (n at -16),
	// while arm64's two-word string is 16 bytes (n at -24). The DIE offsets
	// must track that, so we assert the exact DW_OP_fbreg offset per target.
	// @noinline keeps f a real frame: the subject is f's own DW_OP_fbreg
	// offsets, which do not exist once it is substituted into main.
	src := "@noinline function f(s: string, n: i32): i32 {\n    var m: i32 = n + 1;\n    return m + s.len();\n}\nfunction main(): i32 {\n    return f(\"hi\", 41);\n}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// n / m: {DW_TAG, expected fbreg offset} per target.
	type want struct {
		tag dwarf.Tag
		off int64
	}
	cases := map[string]map[string]want{
		"x86-64-linux": {"n": {dwarf.TagFormalParameter, -16}, "m": {dwarf.TagVariable, -24}},
		"arm64-linux":  {"n": {dwarf.TagFormalParameter, -24}, "m": {dwarf.TagVariable, -32}},
	}
	for target, wants := range cases {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			if o, err := exec.Command(bin, "-g", "-target", target, "-o", out, p).CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			ef, err := goelf.Open(out)
			if err != nil {
				t.Fatalf("open ELF: %v", err)
			}
			defer ef.Close()
			d, err := ef.DWARF()
			if err != nil {
				t.Fatalf("DWARF(): %v", err)
			}

			// Collect `f`'s variable children.
			r := d.Reader()
			got := map[string]struct {
				tag dwarf.Tag
				typ string
				off int64
				ok  bool
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
				if name, _ := e.Val(dwarf.AttrName).(string); name != "f" {
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
					g := struct {
						tag dwarf.Tag
						typ string
						off int64
						ok  bool
					}{tag: c.Tag}
					if toff, ok := c.Val(dwarf.AttrType).(dwarf.Offset); ok {
						if typ, terr := d.Type(toff); terr == nil {
							g.typ = typ.String()
						}
					}
					if loc, ok := c.Val(dwarf.AttrLocation).([]byte); ok && len(loc) >= 2 && loc[0] == 0x91 {
						g.off, g.ok = sleb128(loc[1:])
					}
					got[name] = g
				}
				break
			}

			// `s` is a string — non-scalar, so it gets no DIE (only n, m do).
			if _, present := got["s"]; present {
				t.Errorf("string param s should have no DIE (non-scalar), got %v", got["s"])
			}
			for name, w := range wants {
				g, present := got[name]
				if !present {
					t.Errorf("missing variable DIE %q (have %v)", name, got)
					continue
				}
				if g.tag != w.tag {
					t.Errorf("%s: tag = %v, want %v", name, g.tag, w.tag)
				}
				if g.typ != "i32" {
					t.Errorf("%s: type = %q, want i32", name, g.typ)
				}
				if !g.ok || g.off != w.off {
					t.Errorf("%s: DW_OP_fbreg offset = %d (ok=%v), want %d", name, g.off, g.ok, w.off)
				}
			}
		})
	}
}

// TestDWARFStructVars is the end-to-end guard for composite (struct) variable
// DIEs (#5537 slice 3): a struct-typed variable gets a DW_TAG_pointer_type →
// DW_TAG_structure_type with a DW_TAG_member per scalar field (name, type,
// byte offset), so gdb/lldb `print *p` shows the fields. (Verified live: gdb
// prints `{x = 7, y = 35}` and `p->x` = 7.)
func TestDWARFStructVars(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\n" +
		"function main(): i32 {\n" +
		"    var pt: Point = Point { x: 7, y: 35 };\n" +
		"    return pt.x + pt.y;\n" +
		"}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			if o, err := exec.Command(bin, "-g", "-target", target, "-o", out, p).CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			ef, err := goelf.Open(out)
			if err != nil {
				t.Fatalf("open ELF: %v", err)
			}
			defer ef.Close()
			d, err := ef.DWARF()
			if err != nil {
				t.Fatalf("DWARF(): %v", err)
			}
			r := d.Reader()
			var ptType dwarf.Type
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
				if name, _ := e.Val(dwarf.AttrName).(string); name != "main" {
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
					if name, _ := c.Val(dwarf.AttrName).(string); name != "pt" {
						continue
					}
					if toff, ok := c.Val(dwarf.AttrType).(dwarf.Offset); ok {
						ptType, _ = d.Type(toff)
					}
				}
				break
			}
			if ptType == nil {
				t.Fatal("no DW_AT_type for pt")
			}
			ptr, ok := ptType.(*dwarf.PtrType)
			if !ok {
				t.Fatalf("pt type = %T (%v), want *dwarf.PtrType", ptType, ptType)
			}
			st, ok := ptr.Type.(*dwarf.StructType)
			if !ok {
				t.Fatalf("pt points to %T, want *dwarf.StructType", ptr.Type)
			}
			if st.StructName != "Point" {
				t.Errorf("struct name = %q, want Point", st.StructName)
			}
			want := []struct {
				name string
				off  int64
			}{{"x", 0}, {"y", 4}}
			if len(st.Field) != 2 {
				t.Fatalf("Point has %d fields, want 2: %v", len(st.Field), st.Field)
			}
			for i, w := range want {
				f := st.Field[i]
				if f.Name != w.name || f.ByteOffset != w.off {
					t.Errorf("field %d = {%s @%d}, want {%s @%d}", i, f.Name, f.ByteOffset, w.name, w.off)
				}
				if f.Type.String() != "i32" {
					t.Errorf("field %s type = %q, want i32", f.Name, f.Type.String())
				}
			}
		})
	}
}

// TestDWARFMixedStructVars guards partial struct DIEs (#5537 slice 3): a struct
// with BOTH scalar and non-scalar fields is still described — its scalar fields
// get member DIEs (at their real layout offsets, which account for the
// non-scalar field's space) while the non-scalar field is omitted. gdb shows
// `{age = 36, score = 99}` for a `Person { name: string, age: i32, score:
// i32 }`. The described members must carry the correct offsets (so the string
// field's slot is skipped), verified on both x86-64 and arm64.
func TestDWARFMixedStructVars(t *testing.T) {
	src := "struct Person { name: string, age: i32, score: i32 }\n" +
		// @noinline: the assertion reads describe's param `p` to reach the struct
		// type, so the parameter DIE has to survive.
		"@noinline function describe(p: Person): i32 { return p.age + p.score; }\n" +
		"function main(): i32 {\n" +
		"    var p: Person = Person { name: \"Ada\", age: 36, score: 99 };\n" +
		"    return describe(p);\n" +
		"}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	spath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(spath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			if o, err := exec.Command(bin, "-g", "-target", target, "-o", out, spath).CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			ef, err := goelf.Open(out)
			if err != nil {
				t.Fatalf("open ELF: %v", err)
			}
			defer ef.Close()
			d, err := ef.DWARF()
			if err != nil {
				t.Fatalf("DWARF(): %v", err)
			}
			// Find `describe`'s param `p` and resolve its pointed-to struct.
			r := d.Reader()
			var st *dwarf.StructType
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
				if name, _ := e.Val(dwarf.AttrName).(string); name != "describe" {
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
					if name, _ := c.Val(dwarf.AttrName).(string); name != "p" {
						continue
					}
					if toff, ok := c.Val(dwarf.AttrType).(dwarf.Offset); ok {
						if typ, _ := d.Type(toff); typ != nil {
							if ptr, ok := typ.(*dwarf.PtrType); ok {
								st, _ = ptr.Type.(*dwarf.StructType)
							}
						}
					}
				}
				break
			}
			if st == nil {
				t.Fatal("no pointer-to-struct type for param p")
			}
			// Only the two scalar fields are described; `name` (string) is omitted.
			byName := map[string]int64{}
			for _, f := range st.Field {
				byName[f.Name] = f.ByteOffset
				if f.Type.String() != "i32" {
					t.Errorf("field %s type = %q, want i32", f.Name, f.Type.String())
				}
			}
			if _, present := byName["name"]; present {
				t.Errorf("string field `name` should be omitted, got member at %d", byName["name"])
			}
			if len(st.Field) != 2 {
				t.Errorf("described %d fields, want 2 (age, score): %v", len(st.Field), st.Field)
			}
			for _, f := range []string{"age", "score"} {
				if _, ok := byName[f]; !ok {
					t.Errorf("missing scalar member %q (have %v)", f, byName)
				}
			}
			// age/score must sit AFTER the string field's slot (offset > 0),
			// proving the layout offsets are real (not a 0-based scalar-only pack).
			if byName["age"] == 0 {
				t.Errorf("age at offset 0 — expected the string field to precede it")
			}
		})
	}
}

// TestDWARFNestedStructVars guards nested-struct member recursion (#5537 slice
// 3 composite): a struct field that is itself a struct is described as a
// pointer-to-struct member (the field holds a pointer to the nested box), so
// gdb `print *r` shows `{origin = 0x…, w = 5, h = 6}` and `print *r.origin`
// derefs to `{x = 3, y = 4}`. The nested `Point` structure_type must be emitted
// with its own scalar members and referenced through a pointer_type; verified
// on both x86-64 and arm64.
func TestDWARFNestedStructVars(t *testing.T) {
	src := "struct Point { x: i32, y: i32 }\n" +
		"struct Rect { origin: Point, w: i32, h: i32 }\n" +
		// @noinline: the assertion reads area's param `r` to reach the nested
		// struct type, so the parameter DIE has to survive.
		"@noinline function area(r: Rect): i32 { return r.w * r.h; }\n" +
		"function main(): i32 {\n" +
		"    var r: Rect = Rect { origin: Point { x: 3, y: 4 }, w: 5, h: 6 };\n" +
		"    return area(r) + r.origin.x;\n" +
		"}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	spath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(spath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			if o, err := exec.Command(bin, "-g", "-target", target, "-o", out, spath).CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			ef, err := goelf.Open(out)
			if err != nil {
				t.Fatalf("open ELF: %v", err)
			}
			defer ef.Close()
			d, err := ef.DWARF()
			if err != nil {
				t.Fatalf("DWARF(): %v", err)
			}
			// Find `area`'s param `r` and resolve its pointed-to Rect struct.
			r := d.Reader()
			var st *dwarf.StructType
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
				if name, _ := e.Val(dwarf.AttrName).(string); name != "area" {
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
					if name, _ := c.Val(dwarf.AttrName).(string); name != "r" {
						continue
					}
					if toff, ok := c.Val(dwarf.AttrType).(dwarf.Offset); ok {
						if typ, _ := d.Type(toff); typ != nil {
							if ptr, ok := typ.(*dwarf.PtrType); ok {
								st, _ = ptr.Type.(*dwarf.StructType)
							}
						}
					}
				}
				break
			}
			if st == nil {
				t.Fatal("no pointer-to-struct type for param r")
			}
			if st.StructName != "Rect" {
				t.Errorf("struct name = %q, want Rect", st.StructName)
			}
			byName := map[string]*dwarf.StructField{}
			for _, f := range st.Field {
				byName[f.Name] = f
			}
			if len(st.Field) != 3 {
				t.Fatalf("Rect described %d fields, want 3 (origin, w, h): %v", len(st.Field), st.Field)
			}
			// origin is a pointer-to-Point struct with its own x/y i32 members.
			origin := byName["origin"]
			if origin == nil {
				t.Fatalf("missing nested member `origin` (have %v)", byName)
			}
			optr, ok := origin.Type.(*dwarf.PtrType)
			if !ok {
				t.Fatalf("origin type = %T, want *dwarf.PtrType", origin.Type)
			}
			pt, ok := optr.Type.(*dwarf.StructType)
			if !ok {
				t.Fatalf("origin points to %T, want *dwarf.StructType", optr.Type)
			}
			if pt.StructName != "Point" {
				t.Errorf("origin struct = %q, want Point", pt.StructName)
			}
			if len(pt.Field) != 2 || pt.Field[0].Name != "x" || pt.Field[1].Name != "y" {
				t.Errorf("Point members = %v, want x,y", pt.Field)
			}
			// w/h are scalars sitting after the nested-struct pointer slot.
			for _, n := range []string{"w", "h"} {
				f := byName[n]
				if f == nil {
					t.Errorf("missing scalar member %q", n)
					continue
				}
				if f.Type.String() != "i32" {
					t.Errorf("field %s type = %q, want i32", n, f.Type.String())
				}
			}
		})
	}
}

// TestDWARFEnumVars guards enum type DIEs (#5537 slice 3 composite): a
// payloadless (C-style) enum variable is described as a pointer to a
// DW_TAG_enumeration_type whose DW_TAG_enumerator children map each variant's
// tag (its declaration index) to its name. A Fern payloadless enum value is a
// pointer to a 4-byte i32 tag sentinel, so gdb `print *d` derefs and renders
// the tag as the variant name (e.g. `South`). Verified on both backends.
func TestDWARFEnumVars(t *testing.T) {
	src := "enum Direction { North, East, South, West }\n" +
		// @noinline: the assertion reads turn's param `d` to reach the enum type,
		// so the parameter DIE has to survive.
		"@noinline function turn(d: Direction): i32 {\n" +
		"    match (d) {\n" +
		"        North => { return 0; },\n" +
		"        East => { return 1; },\n" +
		"        South => { return 2; },\n" +
		"        West => { return 3; },\n" +
		"    }\n" +
		"    return -1;\n" +
		"}\n" +
		"function main(): i32 {\n" +
		"    var d: Direction = South;\n" +
		"    return turn(d);\n" +
		"}\n"
	bin := buildFernCLI(t)
	dir := t.TempDir()
	spath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(spath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, "g-"+target+".bin")
			if o, err := exec.Command(bin, "-g", "-target", target, "-o", out, spath).CombinedOutput(); err != nil {
				t.Fatalf("-g build: %v\n%s", err, o)
			}
			ef, err := goelf.Open(out)
			if err != nil {
				t.Fatalf("open ELF: %v", err)
			}
			defer ef.Close()
			d, err := ef.DWARF()
			if err != nil {
				t.Fatalf("DWARF(): %v", err)
			}
			// Find `turn`'s param `d` and resolve its pointed-to enum type.
			r := d.Reader()
			var et *dwarf.EnumType
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
				if name, _ := e.Val(dwarf.AttrName).(string); name != "turn" {
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
					if name, _ := c.Val(dwarf.AttrName).(string); name != "d" {
						continue
					}
					if toff, ok := c.Val(dwarf.AttrType).(dwarf.Offset); ok {
						if typ, _ := d.Type(toff); typ != nil {
							if ptr, ok := typ.(*dwarf.PtrType); ok {
								et, _ = ptr.Type.(*dwarf.EnumType)
							}
						}
					}
				}
				break
			}
			if et == nil {
				t.Fatal("no pointer-to-enum type for param d")
			}
			if et.EnumName != "Direction" {
				t.Errorf("enum name = %q, want Direction", et.EnumName)
			}
			want := []struct {
				name string
				val  int64
			}{{"North", 0}, {"East", 1}, {"South", 2}, {"West", 3}}
			if len(et.Val) != len(want) {
				t.Fatalf("enum has %d enumerators, want %d: %v", len(et.Val), len(want), et.Val)
			}
			for i, w := range want {
				ev := et.Val[i]
				if ev.Name != w.name || ev.Val != w.val {
					t.Errorf("enumerator %d = {%s = %d}, want {%s = %d}", i, ev.Name, ev.Val, w.name, w.val)
				}
			}
		})
	}
}

// sleb128 decodes a signed LEB128 value from the front of b.
func sleb128(b []byte) (int64, bool) {
	var result int64
	var shift uint
	for i, by := range b {
		result |= int64(by&0x7f) << shift
		shift += 7
		if by&0x80 == 0 {
			if shift < 64 && by&0x40 != 0 {
				result |= -1 << shift
			}
			_ = i
			return result, true
		}
	}
	return 0, false
}
