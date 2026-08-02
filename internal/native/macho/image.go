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
	m.u32(mhNoUndefs | mhDyldLink | mhTwoLevel | mhPIE)
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

// buildVersion writes an LC_BUILD_VERSION command declaring the image as
// macOS. Apple Silicon's kernel refuses to exec an arm64 main executable that
// names no platform: it SIGKILLs the process at exec (exit 137, no crash
// report, nothing in the system log), which is indistinguishable from a
// spurious kill unless you know to look for this command. A valid ad-hoc
// signature is NOT sufficient on its own — this image had one and was still
// rejected. ntools is 0: the tool-version list is informational.
func (m *image) buildVersion() {
	m.u32(lcBuildVersion)
	m.u32(buildVersionLen)
	m.u32(platformMacOS)
	m.u32(minOSVersion)
	m.u32(sdkVersion)
	m.u32(0) // ntools
	m.ncmds++
}

// dylinker writes an LC_LOAD_DYLINKER naming /usr/lib/dyld, and dylib writes an
// LC_LOAD_DYLIB. Both are lc_str commands: a uint32 offset from the start of
// the command to the NUL-terminated path, then the path padded to 8 bytes.
func (m *image) lcStr(cmd uint32, path string) {
	size := lcStrCmdLen(cmd, path)
	m.u32(cmd)
	m.u32(uint32(size))
	if cmd == lcLoadDylib {
		m.u32(24) // name offset: past cmd/cmdsize/offset/timestamp/versions
		m.u32(0)  // timestamp
		m.u32(0)  // current_version
		m.u32(0)  // compatibility_version
	} else {
		m.u32(12) // name offset: past cmd/cmdsize/offset
	}
	copy(m.b[m.off:], path)
	m.off += size - lcStrFixedLen(cmd)
	m.ncmds++
}

// main writes LC_MAIN. entryoff is a FILE offset from the start of the mach
// header, not a VM address — dyld adds the image's load address itself, which
// is what makes the entry survive a slide.
func (m *image) main(entryoff uint64) {
	m.u32(lcMain)
	m.u32(mainCmdLen)
	m.u64(entryoff)
	m.u64(0) // stacksize: 0 means the system default
	m.ncmds++
}

// dysymtab writes an LC_DYSYMTAB with every index and count zero except the
// local-symbol range, which covers the whole LC_SYMTAB. dyld requires the
// command to be present on a two-level image; the content is degenerate here
// because nothing is exported and nothing is imported.
func (m *image) dysymtab(nlocal uint32) {
	m.u32(lcDysymtab)
	m.u32(dysymtabCmdLen)
	m.u32(0)      // ilocalsym
	m.u32(nlocal) // nlocalsym
	for i := 0; i < 16; i++ {
		m.u32(0) // extdef/undef ranges, toc, modtab, refsyms, indirect, ext/loc rel
	}
	m.ncmds++
}

// dyldInfo writes LC_DYLD_INFO_ONLY. Only the rebase stream is populated: the
// image imports nothing, exports nothing and binds nothing, so every other
// off/size pair is zero.
func (m *image) dyldInfo(rebaseOff, rebaseSize uint32) {
	m.u32(lcDyldInfoOnly)
	m.u32(dyldInfoCmdLen)
	m.u32(rebaseOff)
	m.u32(rebaseSize)
	for i := 0; i < 8; i++ {
		m.u32(0) // bind, weak_bind, lazy_bind, export
	}
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
