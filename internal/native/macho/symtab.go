package macho

import (
	"encoding/binary"
	"sort"
	"strings"
)

// Sym is one static symbol-table entry: a function name and its absolute
// virtual address. Emitted into LC_SYMTAB under `fern -g` so lldb, nm, and a
// crash backtrace can resolve a code address to its Fern function — the Mach-O
// counterpart of the ELF writer's .symtab (#5537 slice 1 for arm64-darwin).
type Sym struct {
	Name  string
	Value uint64
}

// FuncSyms turns an assembler's label→absolute-vaddr map into a sorted []Sym,
// mirroring elf.FuncSyms: assembler-local labels (any name beginning with ".")
// are dropped — they are branch targets, not functions — and the rest are
// sorted by address. Mach-O nlist entries carry no size field, so unlike the
// ELF path no inter-symbol gap is computed.
func FuncSyms(labels map[string]uint64, textEndVAddr uint64) []Sym {
	_ = textEndVAddr // accepted for signature parity with elf.FuncSyms
	syms := make([]Sym, 0, len(labels))
	for name, v := range labels {
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		syms = append(syms, Sym{Name: name, Value: v})
	}
	sort.Slice(syms, func(i, j int) bool {
		if syms[i].Value != syms[j].Value {
			return syms[i].Value < syms[j].Value
		}
		return syms[i].Name < syms[j].Name
	})
	return syms
}

// nlist type bits (mach-o/nlist.h) for a defined external symbol in __text.
const (
	nSect = 0x0e // N_SECT: symbol defined in the section n_sect
	nExt  = 0x01 // N_EXT: external (global) symbol
)

// buildSymtab encodes the nlist_64 array and the string table for syms. Each
// nlist_64 is 16 bytes: n_strx(u32) n_type(u8) n_sect(u8) n_desc(u16)
// n_value(u64). Every function is a defined external symbol (n_type =
// N_SECT|N_EXT) in the first section, __text (n_sect = 1). The string table
// starts with a NUL so index 0 is the empty string, then each name follows
// NUL-terminated.
func buildSymtab(syms []Sym) (nlists, strtab []byte) {
	strtab = []byte{0}
	nlists = make([]byte, 0, len(syms)*nlistLen)
	for _, s := range syms {
		strx := len(strtab)
		strtab = append(strtab, s.Name...)
		strtab = append(strtab, 0)
		var nl [nlistLen]byte
		binary.LittleEndian.PutUint32(nl[0:], uint32(strx)) // n_strx
		nl[4] = nSect | nExt                                // n_type
		nl[5] = 1                                           // n_sect: __text
		binary.LittleEndian.PutUint16(nl[6:], 0)            // n_desc
		binary.LittleEndian.PutUint64(nl[8:], s.Value)      // n_value
		nlists = append(nlists, nl[:]...)
	}
	return nlists, strtab
}
