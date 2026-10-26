package e2ecompiler

import (
	"debug/dwarf"
	goelf "debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelfHostDWARFSplitParams(t *testing.T) {
	cli := newStrictCLI(t)
	const source = `import "std/time";
struct Point { x: i32, y: i32 }
struct FloatPoint { x: f64, y: f64 }
@noinline function sum(p: Point, k: i32): i32 { return p.x + p.y + k; }
@noinline function total(p: FloatPoint): f64 { return p.x + p.y; }
@noinline function read(d: Option[Duration], k: i32): i32 {
  match (d) {
    Some(t) => { return (t.sec as i32) + t.nsec + k; },
    None => { return k; }
  }
}
function main(): i32 {
  let n = args().len();
  let i = sum(Point { x: n, y: n + 1 }, n);
  let j = read(Some(Duration { sec: n as i64, nsec: n + 1 }), n);
  return i + j + (total(FloatPoint { x: n as f64 + 0.25, y: n as f64 + 1.5 }) as i32);
}
`
	entry := filepath.Join(t.TempDir(), "pieces.fern")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	bins := map[string]string{}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			path := filepath.Join(filepath.Dir(entry), "pieces-"+target)
			if out, err := runX86_64Bin(cli.runner, cli.bin, "-g", "-target", target, "-o", path, entry, cli.stdlib).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			bins[target] = path
			f, err := goelf.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			d, err := f.DWARF()
			if err != nil {
				t.Fatal(err)
			}
			point, ok := dwarfVarType(t, d, "sum", "p").(*dwarf.StructType)
			if !ok {
				t.Fatal("sum.p has no value record type")
			}
			checkDwarfStruct(t, point, "Point", 16, [][3]string{{"x", "0", "i32"}, {"y", "8", "i32"}})
			option, ok := dwarfVarType(t, d, "read", "d").(*dwarf.StructType)
			if !ok || option.ByteSize != 24 || len(option.Field) != 2 || option.Field[0].Name != "some" || option.Field[0].ByteOffset != 0 || option.Field[1].Name != "value" || option.Field[1].ByteOffset != 8 {
				t.Fatalf("read.d has an invalid optional record layout: %v", option)
			}
			duration, ok := option.Field[1].Type.(*dwarf.StructType)
			if !ok {
				t.Fatal("read.d.value has no value record type")
			}
			checkDwarfStruct(t, duration, "Duration", 16, [][3]string{{"sec", "0", "i64"}, {"nsec", "8", "i32"}})
			floating, ok := dwarfVarType(t, d, "total", "p").(*dwarf.StructType)
			if !ok {
				t.Fatal("total.p has no value record type")
			}
			checkDwarfStruct(t, floating, "FloatPoint", 16, [][3]string{{"x", "0", "f64"}, {"y", "8", "f64"}})
			for _, param := range [][2]string{{"sum", "p"}, {"read", "d"}, {"total", "p"}} {
				want := 2
				if param[0] == "read" {
					want = 3
				}
				checkDwarfParamPieces(t, f, d, param[0], param[1], want)
			}
		})
	}
	gdb, err := exec.LookPath("gdb")
	if err != nil || runtime.GOARCH != "amd64" {
		t.Skip("GDB requires an amd64 host")
	}
	if t.Failed() {
		return
	}
	out, err := exec.Command(gdb, "-batch", "-ex", "break sum", "-ex", "break read", "-ex", "break total", "-ex", "run", "-ex", "print p", "-ex", "continue", "-ex", "print d", "-ex", "continue", "-ex", "print p", bins["x86-64-linux"]).CombinedOutput()
	if err != nil {
		t.Fatalf("gdb: %v\n%s", err, out)
	}
	for _, want := range []string{"x = 1, y = 2", "some = true", "sec = 1", "nsec = 2", "x = 1.25, y = 2.5"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("gdb did not read %q\n%s", want, out)
		}
	}
}

func checkDwarfParamPieces(t *testing.T, f *goelf.File, d *dwarf.Data, fn, name string, want int) {
	t.Helper()
	r := d.Reader()
	current := ""
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if e == nil {
			t.Fatalf("no parameter %s.%s", fn, name)
		}
		if e.Tag == dwarf.TagSubprogram {
			current, _ = e.Val(dwarf.AttrName).(string)
		}
		if current != fn || e.Val(dwarf.AttrName) != name || e.Tag != dwarf.TagFormalParameter {
			continue
		}
		offset, ok := e.Val(dwarf.AttrLocation).(int64)
		if !ok {
			t.Fatalf("%s.%s has no location list", fn, name)
		}
		loc, err := f.Section(".debug_loc").Data()
		if err != nil {
			t.Fatal(err)
		}
		complete := false
		for at := int(offset); ; {
			if at < 0 || at+16 > len(loc) {
				t.Fatal("truncated location range")
			}
			lo, hi := binary.LittleEndian.Uint64(loc[at:]), binary.LittleEndian.Uint64(loc[at+8:])
			at += 16
			if lo == 0 && hi == 0 {
				break
			}
			if at+2 > len(loc) {
				t.Fatal("truncated location length")
			}
			n := int(binary.LittleEndian.Uint16(loc[at:]))
			at += 2
			if at+n > len(loc) {
				t.Fatal("truncated location expression")
			}
			expr := loc[at : at+n]
			at += n
			count, present := 0, 0
			for i := 0; i < len(expr); {
				op := expr[i]
				i++
				switch {
				case op == 0x93:
					if i >= len(expr) || expr[i] != 8 {
						t.Fatal("invalid piece width")
					}
					i++
					count++
				case op == 0x9e:
					if i >= len(expr) || int(expr[i])+i+1 > len(expr) {
						t.Fatal("invalid implicit value")
					}
					i += int(expr[i]) + 1
					present++
				case op == 0x90:
					for {
						if i >= len(expr) {
							t.Fatal("truncated register number")
						}
						b := expr[i]
						i++
						if b&0x80 == 0 {
							break
						}
					}
					present++
				case op >= 0x50 && op <= 0x6f:
					present++
				case op >= 0x70 && op <= 0x8f:
					for {
						if i >= len(expr) {
							t.Fatal("truncated register offset")
						}
						b := expr[i]
						i++
						if b&0x80 == 0 {
							break
						}
					}
					present++
				default:
					t.Fatalf("unexpected location opcode %#x", op)
				}
			}
			if count != want {
				t.Fatalf("%s.%s has %d pieces, want %d", fn, name, count, want)
			}
			complete = complete || present == want
		}
		if !complete {
			t.Fatalf("%s.%s has no complete value range", fn, name)
		}
		return
	}
}

func TestSelfHostDWARFReboundRecord(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("GDB requires an amd64 host")
	}
	gdb, err := exec.LookPath("gdb")
	if err != nil {
		t.Skip("GDB is unavailable")
	}
	cli := newStrictCLI(t)
	const source = `struct Point { x: i32, y: i32 }
@noinline function checkpoint(n: i32): i32 { print("tick"); return n; }
function main(): i32 {
  let n = args().len() + 8;
  let p = Point { x: 1, y: 2 };
  checkpoint(0);
  p = Point { x: n, y: n + 1 };
  checkpoint(1);
  return p.x + p.y;
}
`
	dir := t.TempDir()
	entry, bin := filepath.Join(dir, "rebound.fern"), filepath.Join(dir, "rebound")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runX86_64Bin(cli.runner, cli.bin, "-g", "-target", "x86-64-linux", "-o", bin, entry, cli.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(gdb, "-batch", "-ex", "break checkpoint", "-ex", "run", "-ex", "continue", "-ex", "up", "-ex", "print p", bin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "x = 9, y = 10") {
		t.Fatalf("GDB must show the current record value: %v\n%s", err, out)
	}
}
