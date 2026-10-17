package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostDebugPreservesNativeCode(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for name, source := range map[string]string{
		"leaf-frame":             leafFrameSource(),
		"and-not":                andNotProgram,
		"scalar-record-wide":     scalarRecordWide,
		"scalar-record-phis":     scalarRecordSmallPhis,
		"scalar-record-pressure": scalarRecordPressure,
		"append-report":          selfHostAppendReportSrc,
		"abort-chain":            deepAbortSrc,
		"tail-call":              ssaTailCallProg,
		"register-pressure":      dwarfVarsSrc,
		"extra-caller-registers": extraCallerSource(9, false),
		"extra-caller-spills":    extraCallerSource(20, false),
		"extra-caller-rhs":       extraCallerSource(20, true),
		"extra-caller-record":    extraCallerReadSource(9, "record"),
		"extra-caller-array":     extraCallerReadSource(20, "array-variable"),
		"extra-caller-string":    extraCallerReadSource(20, "string-constant"),
		"enum-locals":            dwarfEnumsSrc,
		"partial-enum": `enum Direction { North, East, South, West }
@noinline function pick(d: Direction): i32 { match (d) { South => { return 2; }, _ => { return 3; }, } return 0; }
function main(): i32 { let d: Direction = South; return pick(d); }`,
		"aggregate-locals": dwarfStructsSrc,
		"scalar-option": `import "std/option";
@noinline function maybe(x: i32): Option[i32] { if (x > 0) { return Some(x + 4); } return None; }
function main(): i32 { let v: Option[i32] = maybe(args().len()); return v.unwrap_or(17); }`,
		"scalar-record": `struct Pair { first: i32, second: i32 }
function pair(x: i32): Pair { return Pair { first: x, second: x + 3 }; }
function main(): i32 {
  let value: Pair = pair(args().len());
  return value.first + value.second;
}`,
	} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
			t.Run(name+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				src := filepath.Join(dir, "main.fern")
				if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				var text [][]byte
				for _, debug := range []bool{false, true} {
					out := filepath.Join(dir, "program")
					args := []string{"-target", target, "-o", out, src, cli.stdlib}
					if debug {
						args = append([]string{"-g"}, args...)
					}
					if msg, err := runX86_64Bin(cli.runner, cli.bin, args...).CombinedOutput(); err != nil {
						t.Fatalf("compile (debug=%v): %v\n%s", debug, err, msg)
					}
					text = append(text, nativeProgramText(t, out, target))
				}
				if !bytes.Equal(text[0], text[1]) {
					t.Errorf("-g changes native instructions: plain %d bytes, debug %d bytes", len(text[0]), len(text[1]))
				}
			})
		}
	}
}

func nativeProgramText(t *testing.T, path, target string) []byte {
	t.Helper()
	if target == "arm64-darwin" {
		f, err := macho.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		s := f.Section("__text")
		if s == nil {
			t.Fatal("missing __text")
		}
		data, err := s.Data()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	f, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Plain ELF images have no section table. Compare the executable load's
	// payload after its headers, including its unwind information.
	header, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	start := binary.LittleEndian.Uint64(header[32:40]) + uint64(binary.LittleEndian.Uint16(header[54:56]))*uint64(binary.LittleEndian.Uint16(header[56:58]))
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 {
			data, err := io.ReadAll(p.Open())
			if err != nil {
				t.Fatal(err)
			}
			if start < p.Off || start-p.Off >= uint64(len(data)) {
				t.Fatal("invalid executable payload offset")
			}
			return data[start-p.Off:]
		}
	}
	t.Fatal("missing executable load")
	return nil
}
