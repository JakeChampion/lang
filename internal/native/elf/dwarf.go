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
	dwAtStmtList = 0x10
	dwAtLowPC    = 0x11
	dwAtHighPC   = 0x12
	dwAtLanguage = 0x13
	dwAtCompDir  = 0x1b
	dwAtProducer = 0x25

	dwFormAddr      = 0x01
	dwFormData2     = 0x05
	dwFormString    = 0x08
	dwFormSecOffset = 0x17

	dwLangC99 = 0x000c // generic C-like; no DWARF language code exists for Fern
)

// appendSLEB appends v as signed LEB128.
func appendSLEB(b []byte, v int64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && c&0x40 == 0) || (v == -1 && c&0x40 != 0) {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

// LineRow is one (absolute address → source line) row for a per-statement
// .debug_line table (#5537 slice 2 finer path). Rows must be sorted by Addr.
type LineRow struct {
	Addr uint64
	Line int
}

// buildDebugLineRows encodes a DWARF v4 .debug_line section as ONE sequence
// over per-statement rows (sorted by Addr): set_address to the first row,
// then advance_pc + advance_line + copy for each, and a final advance to
// textHi + end_sequence. Each row's line holds until the next row's address,
// so gaps inherit the preceding statement's line. srcFile names file 1.
func buildDebugLineRows(rows []LineRow, textLo, textHi uint64, srcFile string) []byte {
	var prog []byte
	if len(rows) > 0 {
		curAddr := rows[0].Addr
		curLine := 1
		prog = append(prog, 0x00, 9, 0x02) // ext: DW_LNE_set_address
		prog = le64(prog, curAddr)
		for _, r := range rows {
			if r.Addr > curAddr {
				prog = append(prog, 0x02) // DW_LNS_advance_pc
				prog = appendULEB(prog, r.Addr-curAddr)
				curAddr = r.Addr
			}
			if r.Line != curLine {
				prog = append(prog, 0x03) // DW_LNS_advance_line
				prog = appendSLEB(prog, int64(r.Line)-int64(curLine))
				curLine = r.Line
			}
			prog = append(prog, 0x01) // DW_LNS_copy
		}
		if textHi > curAddr {
			prog = append(prog, 0x02) // DW_LNS_advance_pc
			prog = appendULEB(prog, textHi-curAddr)
		}
		prog = append(prog, 0x00, 1, 0x01) // ext: DW_LNE_end_sequence
	}
	return dwarfLineSection(prog, srcFile)
}

// dwarfLineSection wraps a line-number program `prog` in the DWARF v4
// .debug_line unit header (version, standard opcode lengths, a single source
// file named srcFile).
func dwarfLineSection(prog []byte, srcFile string) []byte {
	var hdr []byte
	hdr = append(hdr, 1) // minimum_instruction_length
	hdr = append(hdr, 1) // maximum_operations_per_instruction (v4)
	hdr = append(hdr, 1) // default_is_stmt
	lineBase := int8(-5)
	hdr = append(hdr, byte(lineBase)) // line_base (0xFB)
	hdr = append(hdr, 14)             // line_range
	hdr = append(hdr, 13)             // opcode_base
	// standard_opcode_lengths for opcodes 1..12.
	hdr = append(hdr, 0, 1, 1, 1, 1, 0, 0, 0, 1, 0, 0, 1)
	hdr = append(hdr, 0) // include_directories: none (terminator)
	// file_names: one entry {name, dir=0, mtime=0, size=0}, then terminator.
	hdr = append(hdr, srcFile...)
	hdr = append(hdr, 0)
	hdr = appendULEB(hdr, 0) // dir index
	hdr = appendULEB(hdr, 0) // mtime
	hdr = appendULEB(hdr, 0) // size
	hdr = append(hdr, 0)     // end of file_names

	var body []byte
	body = le16(body, 4)                // version
	body = le32(body, uint32(len(hdr))) // header_length (to start of program)
	body = append(body, hdr...)
	body = append(body, prog...)

	out := le32(nil, uint32(len(body))) // unit_length
	return append(out, body...)
}

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
// (with children), abbrev 2 = subprogram (no children). When hasLines, the CU
// carries DW_AT_stmt_list pointing at the .debug_line table.
func buildDebugAbbrev(hasLines bool) []byte {
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
	if hasLines {
		attr(dwAtStmtList, dwFormSecOffset)
	}
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
// [textLo, textHi) followed by one subprogram DIE per symbol. When hasLines,
// the CU adds DW_AT_stmt_list (offset 0 into .debug_line).
func buildDebugInfo(syms []Sym, textLo, textHi uint64, name string, hasLines bool) []byte {
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
	if hasLines {
		die = le32(die, 0) // DW_AT_stmt_list → .debug_line offset 0
	}
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
