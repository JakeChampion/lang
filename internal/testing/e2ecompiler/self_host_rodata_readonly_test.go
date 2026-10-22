package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostRodataReadOnly holds a program's read-only constants to a
// mapping nothing can write: the bytes of a string literal lie in a loaded
// segment without write permission, on every native image writer.
func TestSelfHostRodataReadOnly(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const probe = "fern-rodata-probe-8c41"
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := "function main(): i32 {\n  print(\"" + probe + "\");\n  return 0;\n}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-android", "arm64-darwin"} {
		t.Run(target, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "prog")
			if msg, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, msg)
			}
			img, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			at := bytes.Index(img, []byte(probe))
			if at < 0 {
				t.Fatalf("the literal %q is not in the image", probe)
			}
			if target == "arm64-darwin" {
				checkMachOReadOnly(t, bin, uint64(at))
			} else {
				checkELFReadOnly(t, bin, uint64(at))
			}
		})
	}
}

func checkELFReadOnly(t *testing.T, bin string, at uint64) {
	t.Helper()
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && at >= p.Off && at < p.Off+p.Filesz {
			if p.Flags&elf.PF_W != 0 {
				t.Errorf("the literal is in a writable segment (flags %v)", p.Flags)
			}
			return
		}
	}
	t.Errorf("no loaded segment holds file offset %#x", at)
}

func checkMachOReadOnly(t *testing.T, bin string, at uint64) {
	t.Helper()
	f, err := macho.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const vmProtWrite = 2
	for _, l := range f.Loads {
		s, ok := l.(*macho.Segment)
		if ok && s.Filesz > 0 && at >= s.Offset && at < s.Offset+s.Filesz {
			if s.Prot&vmProtWrite != 0 {
				t.Errorf("the literal is in writable segment %s", s.Name)
			}
			return
		}
	}
	t.Errorf("no segment holds file offset %#x", at)
}
