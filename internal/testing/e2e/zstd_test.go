package e2e

import (
	"bufio"
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std/zstd's encoder on the native backends, its frames read by Go's own
// zstd decoder: numbered lines, noise (raw blocks), a run of one byte
// (run-length blocks), and lines either side of 128 KiB of one byte, the
// second block matching into the first.
func TestZstdEncodeAgainstGo(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "zstd_encode.fern")
	if err := os.WriteFile(srcPath, []byte(zstdEncodeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, be := range nativeBackends() {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_zstd.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v", err)
			}
			checkZstdFrames(t, stdout.String())
		})
	}
}

const zstdEncodeSource = `import "std/zstd";
import "std/hex";
import "core/int";

function lines(n: i32): u8[] {
  let out: u8[] = [];
  let i: i32 = 0;
  while (i < n) {
    for b in ("line " + int.int_to_string(i) + " of the corpus\n").bytes() {
      out = out.append(b);
    }
    i = i + 1;
  }
  return out;
}

function lcg_bytes(n: i32): u8[] {
  let out: u8[] = [];
  let x: i32 = 7;
  let i: i32 = 0;
  while (i < n) {
    x = x * 1103515245 + 12345 & 2147483647;
    out = out.append((x >> 16 & 255) as u8);
    i = i + 1;
  }
  return out;
}

function main(): i32 {
  let run: u8[] = [];
  while (run.len() < 300000) {
    run = run.append(97 as u8);
  }
  let mixed: u8[] = lines(300);
  while (mixed.len() < 131072 + 6790) {
    mixed = mixed.append(97 as u8);
  }
  for b in lines(300) {
    mixed = mixed.append(b);
  }
  let ins: u8[][] = [lines(3000), lcg_bytes(5000), run, mixed];
  for x in ins {
    print(hex.encode(zstd.compress(x)));
  }
  return 0;
}
`

func zstdExpected() [][]byte {
	lines := func(n int) []byte {
		var b bytes.Buffer
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "line %d of the corpus\n", i)
		}
		return b.Bytes()
	}
	noise := make([]byte, 5000)
	x := int32(7)
	for i := range noise {
		x = (x*1103515245 + 12345) & 2147483647
		noise[i] = byte(x >> 16 & 255)
	}
	mixed := lines(300)
	for len(mixed) < 131072+6790 {
		mixed = append(mixed, 'a')
	}
	mixed = append(mixed, lines(300)...)
	return [][]byte{lines(3000), noise, bytes.Repeat([]byte("a"), 300000), mixed}
}

func checkZstdFrames(t *testing.T, out string) {
	t.Helper()
	want := zstdExpected()
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	k := 0
	for ; sc.Scan(); k++ {
		frame, err := hex.DecodeString(strings.TrimSpace(sc.Text()))
		if err != nil || k >= len(want) {
			t.Fatalf("frame %d: %v", k, err)
		}
		got, err := zstdDecodeGo(frame, len(want[k]))
		if err != nil {
			t.Fatalf("frame %d: Go's decoder: %v", k, err)
		}
		if !bytes.Equal(got, want[k]) {
			t.Errorf("frame %d: decodes to %d bytes, want the %d it was made from", k, len(got), len(want[k]))
		}
	}
	if k != len(want) {
		t.Fatalf("%d frames, want %d", k, len(want))
	}
}

// zstdDecodeGo decodes a zstd frame with the decoder Go's standard library
// carries for compressed ELF sections: the frame becomes the one section
// of a minimal ELF file, which debug/elf inflates on reading.
func zstdDecodeGo(frame []byte, size int) ([]byte, error) {
	var section bytes.Buffer
	binary.Write(&section, binary.LittleEndian, elf.Chdr64{Type: uint32(elf.COMPRESS_ZSTD), Size: uint64(size), Addralign: 1})
	section.Write(frame)
	names := []byte("\x00.z\x00.shstrtab\x00")
	const ehsize, shentsize = 64, 64
	namesAt := ehsize + section.Len()
	sectionsAt := namesAt + len(names)
	hdr := elf.Header64{Type: uint16(elf.ET_REL), Machine: uint16(elf.EM_X86_64), Version: 1, Shoff: uint64(sectionsAt), Ehsize: ehsize, Shentsize: shentsize, Shnum: 3, Shstrndx: 2}
	copy(hdr.Ident[:], elf.ELFMAG)
	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	var file bytes.Buffer
	binary.Write(&file, binary.LittleEndian, hdr)
	file.Write(section.Bytes())
	file.Write(names)
	binary.Write(&file, binary.LittleEndian, []elf.Section64{
		{},
		{Name: 1, Type: uint32(elf.SHT_PROGBITS), Flags: uint64(elf.SHF_COMPRESSED), Off: ehsize, Size: uint64(section.Len()), Addralign: 1},
		{Name: 4, Type: uint32(elf.SHT_STRTAB), Off: uint64(namesAt), Size: uint64(len(names)), Addralign: 1},
	})
	f, err := elf.NewFile(bytes.NewReader(file.Bytes()))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(f.Section(".z").Open())
}
