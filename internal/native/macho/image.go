package macho

import "encoding/binary"

// image is a little-endian writer over a pre-sized buffer that lays down
// the Mach-O header and load commands, tracking the command count and
// total size to back-patch into the header at the end.
type image struct {
	b         []byte
	off       int
	ncmds     uint32
	cmdsStart int
}

func newImage(b []byte) *image { return &image{b: b} }

func (m *image) u32(v uint32) {
	binary.LittleEndian.PutUint32(m.b[m.off:], v)
	m.off += 4
}

func (m *image) u64(v uint64) {
	binary.LittleEndian.PutUint64(m.b[m.off:], v)
	m.off += 8
}

// name16 writes a 16-byte, NUL-padded segment/section name.
func (m *image) name16(s string) {
	copy(m.b[m.off:m.off+16], s)
	m.off += 16
}

func (m *image) machHeader() {
	m.u32(mhMagic64)
	m.u32(cpuArm64)
	m.u32(cpuSubAll)
	m.u32(mhExecute)
	m.u32(0) // ncmds — patched in done()
	m.u32(0) // sizeofcmds — patched in done()
	m.u32(mhNoUndefs)
	m.u32(0) // reserved
	m.cmdsStart = m.off
}

// segment writes an LC_SEGMENT_64 command header for nsects sections.
func (m *image) segment(name string, vmaddr, vmsize, fileoff, filesize uint64, maxprot, initprot int32, nsects uint32) {
	m.u32(lcSegment64)
	m.u32(uint32(segCmdLen + int(nsects)*sectLen))
	m.name16(name)
	m.u64(vmaddr)
	m.u64(vmsize)
	m.u64(fileoff)
	m.u64(filesize)
	m.u32(uint32(maxprot))
	m.u32(uint32(initprot))
	m.u32(nsects)
	m.u32(0) // flags
	m.ncmds++
}

func (m *image) section(sect, seg string, addr uint64, size int, offset uint32, align uint32, flags uint32) {
	m.name16(sect)
	m.name16(seg)
	m.u64(addr)
	m.u64(uint64(size))
	m.u32(offset)
	m.u32(align)
	m.u32(0) // reloff
	m.u32(0) // nreloc
	m.u32(flags)
	m.u32(0) // reserved1
	m.u32(0) // reserved2
	m.u32(0) // reserved3
}

// segmentText writes __PAGEZERO and the r-x __TEXT segment, which holds
// the Mach-O header + load commands + the __text (code) section. textOff
// is the file offset of code within the segment (== after the header +
// load commands).
func (m *image) segmentText(textOff uint64, textLen int) {
	const (
		sAttrPureInstrs = 0x80000000
		sAttrSomeInstrs = 0x00000400
	)
	textVMSize := uint64(alignUp(int(textOff)+textLen, pageSize))
	m.segment("__PAGEZERO", 0, baseVAddr, 0, 0, 0, 0, 0)
	m.segment("__TEXT", baseVAddr, textVMSize, 0, textVMSize, vmProtRead|vmProtExecute, vmProtRead|vmProtExecute, 1)
	m.section("__text", "__TEXT", baseVAddr+textOff, textLen, uint32(textOff), 2, sAttrPureInstrs|sAttrSomeInstrs)
}

func (m *image) segmentData(vaddr, vmsize, fileoff uint64, dataLen int) {
	m.segment("__DATA", vaddr, vmsize, fileoff, vmsize, vmProtRead|vmProtWrite, vmProtRead|vmProtWrite, 1)
	m.section("__data", "__DATA", vaddr, dataLen, uint32(fileoff), 3, 0)
}

func (m *image) segmentLinkedit(vaddr, vmsize, fileoff uint64, fileLen int) {
	m.segment("__LINKEDIT", vaddr, vmsize, fileoff, uint64(fileLen), vmProtRead, vmProtRead, 0)
}

// symtab writes an LC_SYMTAB command pointing at the nlist_64 array (symoff,
// nsyms) and string table (stroff, strsize), all file offsets into __LINKEDIT.
func (m *image) symtab(symoff, nsyms, stroff, strsize uint32) {
	m.u32(lcSymtab)
	m.u32(symtabCmdLen)
	m.u32(symoff)
	m.u32(nsyms)
	m.u32(stroff)
	m.u32(strsize)
	m.ncmds++
}

func (m *image) unixThread(entry uint64) {
	m.u32(lcUnixThread)
	m.u32(unixThreadLen)
	m.u32(armThreadState64)
	m.u32(armThreadState64Cnt)
	// arm_thread_state64: x0..x28 (29), fp, lr, sp, pc, cpsr, pad.
	// Only pc matters; the kernel sets up sp for the initial stack.
	state := make([]byte, armThreadState64Cnt*4)
	binary.LittleEndian.PutUint64(state[32*8:], entry) // pc is the 33rd u64
	copy(m.b[m.off:], state)
	m.off += len(state)
	m.ncmds++
}

func (m *image) codeSig(dataoff, datasize uint32) {
	m.u32(lcCodeSignature)
	m.u32(codeSigCmdLen)
	m.u32(dataoff)
	m.u32(datasize)
	m.ncmds++
}

// done back-patches ncmds and sizeofcmds into the Mach-O header.
func (m *image) done() {
	binary.LittleEndian.PutUint32(m.b[16:], m.ncmds)
	binary.LittleEndian.PutUint32(m.b[20:], uint32(m.off-m.cmdsStart))
}
