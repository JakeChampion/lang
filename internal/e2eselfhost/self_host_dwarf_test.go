package e2eselfhost

import (
	"debug/dwarf"
	goelf "debug/elf"
	"os"
	"path/filepath"
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
