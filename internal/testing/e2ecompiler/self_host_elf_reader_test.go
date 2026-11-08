package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A reader over std/elf: it prints the header and the PT_LOAD segments of
// the image on its standard input, in the layout Go's debug/elf is compared
// with.
const elfReaderSrc = `import "std/elf";
import "std/io";

function main(): i32 {
  let Ok(image) = io.read_all_stdin_bytes() else {
    print("no input");
    return 1;
  };
  match (elf.parse(image)) {
    Ok(img) => {
      print("kind " + img.kind.to_string() + " machine " + img.machine.to_string() + " entry " + img.entry.to_string());
      for s in img.loads() {
        print("load flags " + s.flags.to_string() + " offset " + s.offset.to_string() + " address " + s.address.to_string() + " physical " + s.physical.to_string() + " file " + s.file_size.to_string() + " memory " + s.memory_size.to_string() + " align " + s.align.to_string());
      }
      return 0;
    },
    Err(e) => {
      print(e.message());
      return 1;
    }
  }
}
`

const elfSampleSrc = `function main(): i32 {
  let xs: i32[] = [];
  for i in 0..10 {
    xs = xs.append(i);
  }
  return xs.len() - 10;
}
`

// debugElfReport is the reader's output as Go's debug/elf sees the image.
func debugElfReport(t *testing.T, path string) string {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("debug/elf: %v", err)
	}
	defer f.Close()
	var out strings.Builder
	fmt.Fprintf(&out, "kind %d machine %d entry %d\n", f.Type, f.Machine, f.Entry)
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD {
			fmt.Fprintf(&out, "load flags %d offset %d address %d physical %d file %d memory %d align %d\n", p.Flags, p.Off, p.Vaddr, p.Paddr, p.Filesz, p.Memsz, p.Align)
		}
	}
	return out.String()
}

// std/elf reads the images the compiler emits for every ELF target the way
// Go's debug/elf does: the header, and every PT_LOAD segment.
func TestSelfHostElfReaderMatchesDebugElf(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	readerSrc := filepath.Join(dir, "reader.fern")
	if err := os.WriteFile(readerSrc, []byte(elfReaderSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := cli.x86Binary(t, readerSrc)
	sample := filepath.Join(dir, "sample.fern")
	if err := os.WriteFile(sample, []byte(elfSampleSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		args   []string
	}{
		{"x86-64-linux", nil},
		{"arm64-linux", nil},
		{"arm64-kernel", []string{"-ram", kernelRAM}},
		{"x86-64-kernel", []string{"-ram", kernelX86RAM}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			image := filepath.Join(t.TempDir(), "image")
			if msg, err := cli.compileCmd(tc.target, image, sample, tc.args...).CombinedOutput(); err != nil {
				t.Fatalf("build %s: %v\n%s", tc.target, err, msg)
			}
			data, err := os.ReadFile(image)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(reader)
			cmd.Stdin = bytes.NewReader(data)
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("reader: %v\n%s", err, got)
			}
			if want := debugElfReport(t, image); string(got) != want {
				t.Errorf("std/elf read:\n%s\ndebug/elf read:\n%s", got, want)
			}
		})
	}
}
