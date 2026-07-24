package elf

// Minimal DWARF v4 debug info: a single compilation-unit DIE plus one
// subprogram DIE per emitted function (name + PC range), emitted under
// `fern -g` alongside the .symtab (#5537 slice 3, sans line table). This
// gives gdb/lldb a real DWARF program — function frames in `bt`, break-by-
// name, `info functions` — on top of the symbol-table names slice 1 already
// provides. It reuses the [Sym] list (name + absolute vaddr + size) the
// symtab is built from, so no source-position plumbing is required; the
// .debug_line address→source-line table is a separate, larger slice.
//
// Names use DW_FORM_string (inline, NUL-terminated), so no .debug_str
// section is needed. Addresses use DW_FORM_addr (absolute), valid for the
// ET_EXEC images the WX writers produce.

// DWARF tag / attribute / form constants used below (DWARF v4, section 7).
const (
	dwTagCompileUnit = 0x11
	dwTagSubprogram  = 0x2e

	dwChildrenYes = 1
	dwChildrenNo  = 0

	dwAtName     = 0x03
	dwAtLowPC    = 0x11
	dwAtHighPC   = 0x12
	dwAtLanguage = 0x13
	dwAtCompDir  = 0x1b
	dwAtProducer = 0x25

	dwFormAddr   = 0x01
	dwFormData2  = 0x05
	dwFormString = 0x08

	dwLangC99 = 0x000c // generic C-like; no DWARF language code exists for Fern
)

// appendULEB appends v as unsigned LEB128.
func appendULEB(b []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			c |= 0x80
		}
		b = append(b, c)
		if v == 0 {
			return b
		}
	}
}

// buildDebugAbbrev is the fixed abbreviation table: abbrev 1 = compile_unit
// (with children), abbrev 2 = subprogram (no children).
func buildDebugAbbrev() []byte {
	var b []byte
	attr := func(at, form uint64) {
		b = appendULEB(b, at)
		b = appendULEB(b, form)
	}
	// abbrev 1: DW_TAG_compile_unit, has children
	b = appendULEB(b, 1)
	b = appendULEB(b, dwTagCompileUnit)
	b = append(b, dwChildrenYes)
	attr(dwAtProducer, dwFormString)
	attr(dwAtLanguage, dwFormData2)
	attr(dwAtName, dwFormString)
	attr(dwAtCompDir, dwFormString)
	attr(dwAtLowPC, dwFormAddr)
	attr(dwAtHighPC, dwFormAddr)
	attr(0, 0) // end of attribute list

	// abbrev 2: DW_TAG_subprogram, no children
	b = appendULEB(b, 2)
	b = appendULEB(b, dwTagSubprogram)
	b = append(b, dwChildrenNo)
	attr(dwAtName, dwFormString)
	attr(dwAtLowPC, dwFormAddr)
	attr(dwAtHighPC, dwFormAddr)
	attr(0, 0)

	b = appendULEB(b, 0) // end of abbreviation table
	return b
}

// buildDebugInfo encodes the .debug_info compilation unit: the CU DIE spanning
// [textLo, textHi) followed by one subprogram DIE per symbol.
func buildDebugInfo(syms []Sym, textLo, textHi uint64, name string) []byte {
	cstr := func(b []byte, s string) []byte {
		b = append(b, s...)
		return append(b, 0)
	}

	var die []byte
	// CU DIE (abbrev 1).
	die = appendULEB(die, 1)
	die = cstr(die, "fern")     // DW_AT_producer
	die = le16(die, dwLangC99)  // DW_AT_language
	die = cstr(die, name)       // DW_AT_name
	die = cstr(die, "")         // DW_AT_comp_dir
	die = le64(die, textLo)     // DW_AT_low_pc
	die = le64(die, textHi)     // DW_AT_high_pc
	// Subprogram DIEs (abbrev 2), children of the CU.
	for _, s := range syms {
		die = appendULEB(die, 2)
		die = cstr(die, s.Name)          // DW_AT_name
		die = le64(die, s.Value)         // DW_AT_low_pc
		die = le64(die, s.Value+s.Size)  // DW_AT_high_pc
	}
	die = appendULEB(die, 0) // null DIE terminates the CU's children

	// CU header (DWARF v4): unit_length is everything after the length field.
	var body []byte
	body = le16(body, 4) // version
	body = le32(body, 0) // debug_abbrev_offset
	body = append(body, 8) // address_size (64-bit)
	body = append(body, die...)

	out := le32(nil, uint32(len(body))) // unit_length
	return append(out, body...)
}
