package e2eharness

import (
	"bytes"
	"encoding/binary"
)

// MachOBindsSymbol reports whether a Mach-O image's LC_DYLD_INFO_ONLY bind
// stream names `sym` (BIND_OPCODE_SET_SYMBOL_TRAILING_FLAGS_IMM carries it
// inline). The native and self-host account-entry tests both read the stream
// through it (#9815).
func MachOBindsSymbol(img []byte, sym string) bool {
	if len(img) < 32 {
		return false
	}
	ncmds := binary.LittleEndian.Uint32(img[16:])
	off := 32
	for i := uint32(0); i < ncmds && off+8 <= len(img); i++ {
		cmd := binary.LittleEndian.Uint32(img[off:])
		size := int(binary.LittleEndian.Uint32(img[off+4:]))
		if cmd == 0x80000022 && off+24 <= len(img) {
			bindOff := int(binary.LittleEndian.Uint32(img[off+16:]))
			bindLen := int(binary.LittleEndian.Uint32(img[off+20:]))
			if bindLen == 0 || bindOff+bindLen > len(img) {
				return false
			}
			return bytes.Contains(img[bindOff:bindOff+bindLen], append([]byte(sym), 0))
		}
		off += size
	}
	return false
}
