package e2ecompiler

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostMachOProfileSymbols(t *testing.T) {
	cli := buildRuntimeCLI(t)
	image := compileCPUProfile(t, cli, `@noinline function probe(x:i32): i32 { return x*7+3; }
function main(): i32 { return probe(args().len()); }`, "arm64-darwin", "", "-g")
	file, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	dir := copySelfHostTree(t)
	const driver = `import "./rtprofilemacho";
function main(): i32 { print(rtprofilemacho.source()); return 0; }`
	if err := os.WriteFile(filepath.Join(dir, "profile_symbols.fern"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	gcc, runner := x86_64Tooling(t)
	bin := buildSelfHostBin(t, gcc, dir, "profile_symbols.fern", "profile_symbols")
	helpers, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("template: %v\n%s", err, helpers)
	}
	for _, name := range []string{"native-layout", "extra-bss", "invalid-symbol-offset"} {
		t.Run(name, func(t *testing.T) {
			raw := bytes.Clone(file)
			// Moving __LINKEDIT by another page models the native writer's
			// file/VM offset difference when zero-initialized data grows.
			for at, n := 32, binary.LittleEndian.Uint32(raw[16:]); n > 0; n-- {
				kind := binary.LittleEndian.Uint32(raw[at:])
				length := int(binary.LittleEndian.Uint32(raw[at+4:]))
				if kind == 25 && name == "extra-bss" {
					segment := string(bytes.TrimRight(raw[at+8:at+24], "\x00"))
					if segment == "__LINKEDIT" {
						addr := binary.LittleEndian.Uint64(raw[at+24:])
						binary.LittleEndian.PutUint64(raw[at+24:], addr+16384)
					}
					if segment == "__DATA" {
						size := binary.LittleEndian.Uint64(raw[at+32:])
						binary.LittleEndian.PutUint64(raw[at+32:], size+16384)
					}
				}
				at += length
			}
			m, err := macho.NewFile(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			base := m.Segment("__TEXT").Addr
			end := base
			for _, load := range m.Loads {
				if seg, ok := load.(*macho.Segment); ok && seg.Addr >= base && seg.Memsz > 0 {
					end = max(end, seg.Addr+seg.Memsz)
				}
			}
			if end-base > 16<<20 {
				t.Fatal("unexpectedly large fixture image")
			}
			mapped := make([]byte, end-base)
			for _, load := range m.Loads {
				if seg, ok := load.(*macho.Segment); ok && seg.Addr >= base && seg.Memsz > 0 && seg.Filesz > 0 {
					copy(mapped[seg.Addr-base:], raw[seg.Offset:seg.Offset+seg.Filesz])
				}
			}
			var address uint64
			for _, symbol := range m.Symtab.Syms {
				if symbol.Name == "__fn_probe" {
					address = symbol.Value
				}
			}
			if address == 0 {
				t.Fatal("probe symbol absent")
			}
			if name == "invalid-symbol-offset" {
				for at, n := 32, binary.LittleEndian.Uint32(mapped[16:]); n > 0; n-- {
					if binary.LittleEndian.Uint32(mapped[at:]) == 2 {
						binary.LittleEndian.PutUint32(mapped[at+8:], ^uint32(0))
					}
					at += int(binary.LittleEndian.Uint32(mapped[at+4:]))
				}
			}
			want := "true"
			if name == "invalid-symbol-offset" {
				want = "false"
			}
			source := string(helpers) + fmt.Sprintf(`
function image_nibble(c: u8): i32 {
  if (c >= b'a') { return c as i32 - 87; }
  return c as i32 - 48;
}
function image_bytes(hex: string): u8[] {
  let out: u8[] = [];
  let i = 0;
  while (i + 1 < hex.len()) {
    out = out.append((image_nibble(hex[i]) * 16 + image_nibble(hex[i + 1])) as u8);
    i = i + 2;
  }
  return out;
}
function main(): i32 {
  let image: string = string_from_bytes_unchecked(image_bytes("%x"));
  let im = __raw_data(image);
  let pc = im + %du64 as usize;
  let size = image.len() as usize;
  if (__fern_profile_macho_image_size(im) != size) { return 1; }
  let found = __fern_profile_name(pc, im, size);
  if ((found != 0) != %s) { return 2; }
  if (found != 0) {
    let expected = "__fn_probe";
    let i = 0;
    while (i < expected.len()) {
      if (__load_u8(found + i) != __load_u8(__raw_data(expected) + i)) { return 3; }
      i = i + 1;
    }
  }
  if (__fern_profile_name(im + size, im, size) != 0) { return 4; }
  if (__fern_profile_name(pc, im, 31) != 0) { return 5; }
  return 42;
}
`, mapped, address-base+1, want)
			program := compileCPUProfile(t, cli, source, "x86-64-linux", "")
			cmd := runX86_64Bin(cli.runner, program)
			out, _ := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 42 {
				t.Fatalf("symbol lookup: %v\n%s", cmd.ProcessState, out)
			}
		})
	}
}
