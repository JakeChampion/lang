package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"testing"
)

// #8112: the self-host Mach-O writer places __TEXT,__eh_frame.
//
// The recorder and the renderer were already there — TestSelfHostCfiMatches-
// NativeArm64Darwin pins the bytes — and this is the container half: the
// section header inside __TEXT, the code-address shift that header causes,
// and __DATA past the unwind data. Getting the image right and then not
// placing it is invisible from the assembler's side, because the bytes it
// rendered are correct and simply never reach the file.

const machoEhFrameDriverMain = `
function main(): i32 {
    var asm: string = "";
    asm = asm + "_main:\n";
    asm = asm + ".cfi_startproc\n";
    asm = asm + "    stp x29, x30, [sp, #-16]!\n";
    asm = asm + ".cfi_def_cfa_offset 16\n";
    asm = asm + ".cfi_offset x29, -16\n";
    asm = asm + ".cfi_offset x30, -8\n";
    asm = asm + "    mov x29, sp\n";
    asm = asm + ".cfi_def_cfa_register x29\n";
    asm = asm + "    mov x0, #7\n";
    asm = asm + "    ldp x29, x30, [sp], #16\n";
    asm = asm + ".cfi_def_cfa sp, 0\n";
    asm = asm + "    mov x16, #1\n";
    asm = asm + "    svc #0x80\n";
    asm = asm + ".cfi_endproc\n";
    asm = asm + "_helper:\n";
    asm = asm + ".cfi_startproc\n";
    asm = asm + "    sub sp, sp, #16\n";
    asm = asm + ".cfi_def_cfa_offset 16\n";
    asm = asm + "    add sp, sp, #16\n";
    asm = asm + ".cfi_def_cfa_offset 0\n";
    asm = asm + "    ret\n";
    asm = asm + ".cfi_endproc\n";
    var p: Arm64GasProg = arm64_gas_program(asm);
    var pa: Arm64Asm = p.asm;
    var ehlen: i32 = arm64_eh_frame_darwin_len(p);
    var tv: i64 = macho_text_vaddr(pa.code.len(), ehlen, p.data.len(), p.bss_size);
    var ev: i64 = macho_eh_vaddr(pa.code.len(), ehlen, p.data.len(), p.bss_size);
    var dv: i64 = macho_data_vaddr(pa.code.len(), ehlen, p.data.len(), p.bss_size);
    var eh: i32[] = arm64_eh_frame_darwin(p, tv, ev);
    p = arm64_gas_link(p, tv, dv);
    var pa2: Arm64Asm = p.asm;
    var none: i32[] = [];
    var bin: i32[] = macho_executable(pa2.code, eh, p.data, "fern", macho_entry_off(pa2), p.bss_size, none);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`

// The same program with every `.cfi_*` line removed: it must produce an image
// with no __eh_frame section at all, and one whose code starts a section
// header earlier.
const machoNoEhFrameDriverMain = `
function main(): i32 {
    var asm: string = "";
    asm = asm + "_main:\n";
    asm = asm + "    stp x29, x30, [sp, #-16]!\n";
    asm = asm + "    mov x29, sp\n";
    asm = asm + "    mov x0, #7\n";
    asm = asm + "    ldp x29, x30, [sp], #16\n";
    asm = asm + "    mov x16, #1\n";
    asm = asm + "    svc #0x80\n";
    asm = asm + "_helper:\n";
    asm = asm + "    sub sp, sp, #16\n";
    asm = asm + "    add sp, sp, #16\n";
    asm = asm + "    ret\n";
    var p: Arm64GasProg = arm64_gas_program(asm);
    var pa: Arm64Asm = p.asm;
    var tv: i64 = macho_text_vaddr(pa.code.len(), 0, p.data.len(), p.bss_size);
    var dv: i64 = macho_data_vaddr(pa.code.len(), 0, p.data.len(), p.bss_size);
    p = arm64_gas_link(p, tv, dv);
    var pa2: Arm64Asm = p.asm;
    var none: i32[] = [];
    var bin: i32[] = macho_executable(pa2.code, none, p.data, "fern", macho_entry_off(pa2), p.bss_size, none);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`

// fdeFunctionStarts walks an __eh_frame image and returns the address each
// FDE's 8-byte pcrel initial_location resolves to. secAddr is where the
// section is loaded, because the field measures from itself.
func fdeFunctionStarts(sec []byte, secAddr uint64) []uint64 {
	var out []uint64
	for off := 0; off+4 <= len(sec); {
		n := int(binary.LittleEndian.Uint32(sec[off:]))
		if n == 0 || off+4+n > len(sec) {
			break
		}
		body := sec[off+4 : off+4+n]
		// A zero CIE pointer marks the CIE itself; anything else is an FDE.
		if len(body) >= 12 && binary.LittleEndian.Uint32(body) != 0 {
			field := secAddr + uint64(off) + 8
			out = append(out, uint64(int64(field)+int64(binary.LittleEndian.Uint64(body[4:]))))
		}
		off += 4 + n
	}
	return out
}

func TestSelfHostMachOPlacesEhFrame(t *testing.T) {
	bin := selfHostMachOBytes(t, "macho_eh", arm64NativeSrc(t)+"\n"+machoEhFrameDriverMain)
	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("self-host output is not a parseable Mach-O: %v", err)
	}
	sec := f.Section("__eh_frame")
	if sec == nil {
		t.Fatal("no __eh_frame section — the unwind image was rendered and then dropped")
	}
	if sec.Seg != "__TEXT" {
		t.Errorf("__eh_frame is in %s, want __TEXT: _dyld_find_unwind_sections looks for it there", sec.Seg)
	}
	text := f.Section("__text")
	if text == nil {
		t.Fatal("no __text section")
	}
	// 8-aligned right after the code, in the same segment: the FDE pointers
	// are 8-byte, and sharing __TEXT is what keeps the R+X mapping libunwind
	// reads the section through.
	wantAddr := (text.Addr + text.Size + 7) &^ 7
	if sec.Addr != wantAddr {
		t.Errorf("__eh_frame at %#x, want %#x — 8-aligned immediately past the %d bytes of code at %#x",
			sec.Addr, wantAddr, text.Size, text.Addr)
	}
	if sec.Size == 0 {
		t.Fatal("__eh_frame is empty")
	}
	if seg := f.Segment("__TEXT"); seg == nil {
		t.Error("no __TEXT segment")
	} else if sec.Offset+uint32(sec.Size) > uint32(seg.Filesz) {
		t.Errorf("__eh_frame ends at file offset %d, past __TEXT's %d bytes on disk — the bytes are not in the file",
			sec.Offset+uint32(sec.Size), seg.Filesz)
	}
	got, err := sec.Data()
	if err != nil {
		t.Fatal(err)
	}
	// Both FDEs resolve to a function start inside __text: _main at the first
	// code byte, _helper after it. A pcrel pointer computed against the wrong
	// base still decodes, it just names an address in nothing.
	starts := fdeFunctionStarts(got, sec.Addr)
	if len(starts) != 2 {
		t.Fatalf("found %d FDEs (%#x), want 2 — one per .cfi_startproc span", len(starts), starts)
	}
	if starts[0] != text.Addr {
		t.Errorf("first FDE describes %#x, want _main at the first code byte %#x", starts[0], text.Addr)
	}
	if starts[1] <= starts[0] || starts[1] >= text.Addr+text.Size || starts[1]%4 != 0 {
		t.Errorf("second FDE describes %#x, want an instruction-aligned address inside __text [%#x, %#x)",
			starts[1], text.Addr, text.Addr+text.Size)
	}

	// The section header costs 80 bytes of load commands, so an image WITHOUT
	// unwind data starts its code exactly that much earlier. If the writer
	// counted the header but never wrote the section (or the reverse), the
	// addresses the assembler resolved @PAGE/@PAGEOFF against would be a
	// section header out from where the code actually lands.
	plain := selfHostMachOBytes(t, "macho_noeh", arm64NativeSrc(t)+"\n"+machoNoEhFrameDriverMain)
	pf, err := macho.NewFile(bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("no-CFI self-host output is not a parseable Mach-O: %v", err)
	}
	if pf.Section("__eh_frame") != nil {
		t.Error("an image with no .cfi_* directives carries an __eh_frame section")
	}
	ptext := pf.Section("__text")
	if ptext == nil {
		t.Fatal("no __text section in the no-CFI image")
	}
	const sectLen = 80
	if text.Addr != ptext.Addr+sectLen {
		t.Errorf("code is at %#x with unwind data and %#x without; want exactly one section header (%d bytes) of difference",
			text.Addr, ptext.Addr, sectLen)
	}
}
