package arm64

import (
	"fmt"
	"sort"
)

// MachOTextLen returns the final size of the .text (code) in bytes. It
// flushes any pending literal pool and plants any branch veneers first,
// so the count is stable; call it before computing the Mach-O layout
// (which needs the code size to place the __DATA segment). A veneering
// failure is held until LinkMachO, which can report it.
func (a *Assembler) MachOTextLen() int {
	a.FlushLiterals()
	a.veneerErr = a.insertVeneers()
	return len(a.insns) * 4
}

// MachODataLen returns the size of the data blob (__const / __data plus the
// .bss reservation) in bytes. It fixes .bss's position, so it must be called
// before LinkMachO resolves any symbol against it — the Mach-O layout needs
// this size up front anyway, which is what makes that ordering natural.
//
// The whole blob is counted, .bss included: unlike the ELF writer, the
// Mach-O container has no zero-fill section here, so the reservation is
// materialised in the file (see the LinkMachO note).
func (a *Assembler) MachODataLen() int {
	a.padForBss()
	return len(a.rodata) + len(a.bss)
}

// MachODataRebaseOffsets returns the offsets, within the data blob, of every
// 8-byte slot holding an ABSOLUTE address (`.quad <symbol>` — jump tables and
// the like). A Mach-O main executable on Apple Silicon must be PIE, so dyld
// slides the image and every one of these needs rebasing by the slide; the
// container turns them into LC_DYLD_INFO_ONLY rebase opcodes. Everything else
// the code generator emits is PC-relative (adrp/@PAGEOFF, b/bl), which is why
// this is the whole list. Sorted, so the opcode stream can be emitted in one
// forward pass.
func (a *Assembler) MachODataRebaseOffsets() []int {
	offs := make([]int, 0, len(a.quadSymFixups))
	for _, f := range a.quadSymFixups {
		offs = append(offs, f.at)
	}
	sort.Ints(offs)
	return offs
}

// LinkMachO resolves all vaddr-dependent fixups for a Mach-O layout where
// code lives at textVAddr (the __TEXT segment) and the data blob lives at
// dataVAddr (a separate __DATA segment, not contiguous with text), then
// returns the text and data blobs. The literal pool must already be
// flushed (MachOTextLen does this); calling FlushLiterals again here is a
// no-op.
// A Mach-O __DATA segment here carries filesize == vmsize, so a .bss
// reservation is written to the file rather than zero-filled by dyld. Laying
// .bss at the tail (as the ELF path needs) does not by itself change that —
// shrinking the segment's filesize means moving __LINKEDIT and recomputing
// the code signature over the smaller file, which is separate work.
func (a *Assembler) LinkMachO(textVAddr, dataVAddr uint64) (text, data []byte, err error) {
	a.padForBss()
	a.FlushLiterals()
	if a.veneerErr != nil {
		return nil, nil, a.veneerErr
	}
	if err := a.insertVeneers(); err != nil {
		return nil, nil, err
	}
	for _, f := range a.litFixups {
		insnAddr := textVAddr + uint64(f.at)*4
		litAddr := textVAddr + uint64(f.poolIdx)*4
		delta := (int64(litAddr) - int64(insnAddr)) / 4
		if delta < -(1<<18) || delta >= 1<<18 {
			return nil, nil, fmt.Errorf("arm64: ldr-literal at insn %d is %d bytes from its pool — outside the ±1 MB imm19 range (missing .ltorg?)", f.at, delta*4)
		}
		imm19 := uint32(int32(delta))
		a.insns[f.at] |= (imm19 & 0x7ffff) << 5
	}

	symVAddr := func(name string) (uint64, bool) {
		s, ok := a.syms[name]
		if !ok {
			return 0, false
		}
		if s.inText {
			return textVAddr + uint64(s.val)*4, true
		}
		if s.inBss {
			return dataVAddr + uint64(a.bssBase()+s.val), true
		}
		return dataVAddr + uint64(s.val), true
	}

	for _, f := range a.fixups {
		target, ok := a.labels[f.label]
		if !ok {
			return nil, nil, fmt.Errorf("arm64: branch to undefined label %q", f.label)
		}
		if err := checkBranchRange(f.kind, target-f.at, f.label); err != nil {
			return nil, nil, err
		}
		off := uint32(target - f.at)
		switch f.kind {
		case branchImm26:
			a.insns[f.at] |= off & 0x03ffffff
		case branchImm19:
			a.insns[f.at] |= (off & 0x7ffff) << 5
		case branchImm14:
			a.insns[f.at] |= (off & 0x3fff) << 5
		}
	}

	for _, f := range a.adrpFixups {
		sv, ok := symVAddr(f.label)
		if !ok {
			return nil, nil, fmt.Errorf("arm64: adrp to undefined symbol %q", f.label)
		}
		insnVAddr := textVAddr + uint64(f.at)*4
		pageDelta := int32(int64(sv>>12) - int64(insnVAddr>>12))
		a.insns[f.at] = ADRP(f.rd, pageDelta)
	}

	for _, f := range a.lo12Fixups {
		sv, ok := symVAddr(f.label)
		if !ok {
			return nil, nil, fmt.Errorf("arm64: @PAGEOFF/:lo12: of undefined symbol %q", f.label)
		}
		a.insns[f.at] = ADDimm(f.rd, f.rn, uint16(sv&0xfff), false)
	}

	for _, f := range a.quadSymFixups {
		sv, ok := symVAddr(f.label)
		if !ok {
			return nil, nil, fmt.Errorf("arm64: .quad of undefined symbol %q", f.label)
		}
		for i := 0; i < 8; i++ {
			a.rodata[f.at+i] = byte(sv >> (8 * i))
		}
	}

	for _, insn := range a.insns {
		text = Put(text, insn)
	}
	return text, append(a.rodata, a.bss...), nil
}
