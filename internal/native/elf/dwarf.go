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
	dwTagCompileUnit      = 0x11
	dwTagSubprogram       = 0x2e
	dwTagBaseType         = 0x24
	dwTagFormalParameter  = 0x05
	dwTagVariable         = 0x34

	dwChildrenYes = 1
	dwChildrenNo  = 0

	dwAtLocation  = 0x02
	dwAtName      = 0x03
	dwAtByteSize  = 0x0b
	dwAtStmtList  = 0x10
	dwAtLowPC     = 0x11
	dwAtHighPC    = 0x12
	dwAtLanguage  = 0x13
	dwAtCompDir   = 0x1b
	dwAtProducer  = 0x25
	dwAtFrameBase = 0x40
	dwAtType      = 0x49
	dwAtEncoding  = 0x3e

	dwFormAddr      = 0x01
	dwFormData1     = 0x0b
	dwFormData2     = 0x05
	dwFormString    = 0x08
	dwFormRef4      = 0x13
	dwFormSecOffset = 0x17
	dwFormExprLoc   = 0x18

	// DWARF base-type encodings (DW_ATE_*) and location opcodes.
	dwAteAddress  = 0x01
	dwAteBoolean  = 0x02
	dwAteFloat    = 0x04
	dwAteSigned   = 0x05
	dwAteUnsigned = 0x07
	dwOpFbreg     = 0x91 // DW_OP_fbreg <sleb offset> — location relative to frame base
	dwOpRegBase   = 0x50 // DW_OP_reg0; frame-base register R is dwOpRegBase+R

	dwLangC99 = 0x000c // generic C-like; no DWARF language code exists for Fern
)

// LocalVar is one source variable (parameter or local) attached to a
// subprogram DIE for gdb/lldb `info args` / `info locals` / `print`. Offset is
// the variable's frame-base-relative byte offset (the DW_OP_fbreg operand);
// TypeKey names a scalar base type ("i32", "f64", "bool", …) — see
// scalarBaseType. Only scalar-typed variables are emitted for now.
type LocalVar struct {
	Name    string
	TypeKey string
	Offset  int
	IsParam bool
}

// baseTypeInfo is a DWARF base type: its byte size and DW_ATE encoding.
type baseTypeInfo struct {
	size byte
	enc  byte
}

// scalarBaseType maps a Fern scalar type key to its DWARF base-type shape.
var scalarBaseType = map[string]baseTypeInfo{
	"i8": {1, dwAteSigned}, "i16": {2, dwAteSigned}, "i32": {4, dwAteSigned}, "i64": {8, dwAteSigned},
	"u8": {1, dwAteUnsigned}, "u16": {2, dwAteUnsigned}, "u32": {4, dwAteUnsigned}, "u64": {8, dwAteUnsigned},
	"usize": {8, dwAteUnsigned}, "isize": {8, dwAteSigned},
	"f32": {4, dwAteFloat}, "f64": {8, dwAteFloat},
	"bool": {1, dwAteBoolean},
}

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

// Abbreviation codes for the fixed table buildDebugAbbrev emits.
const (
	abbrevCompileUnit    = 1
	abbrevSubprogram     = 2 // no children (function with no scalar variables)
	abbrevSubprogramVars = 3 // has children + DW_AT_frame_base
	abbrevBaseType       = 4
	abbrevFormalParam    = 5
	abbrevVariable       = 6
)

// buildDebugAbbrev is the fixed abbreviation table: compile_unit, subprogram
// (with and without variable children), base_type, and formal_parameter /
// variable. When hasLines, the CU carries DW_AT_stmt_list.
func buildDebugAbbrev(hasLines bool) []byte {
	var b []byte
	attr := func(at, form uint64) {
		b = appendULEB(b, at)
		b = appendULEB(b, form)
	}
	// abbrev 1: DW_TAG_compile_unit, has children
	b = appendULEB(b, abbrevCompileUnit)
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
	attr(0, 0)

	// abbrev 2: DW_TAG_subprogram, no children
	b = appendULEB(b, abbrevSubprogram)
	b = appendULEB(b, dwTagSubprogram)
	b = append(b, dwChildrenNo)
	attr(dwAtName, dwFormString)
	attr(dwAtLowPC, dwFormAddr)
	attr(dwAtHighPC, dwFormAddr)
	attr(0, 0)

	// abbrev 3: DW_TAG_subprogram with children + frame base
	b = appendULEB(b, abbrevSubprogramVars)
	b = appendULEB(b, dwTagSubprogram)
	b = append(b, dwChildrenYes)
	attr(dwAtName, dwFormString)
	attr(dwAtLowPC, dwFormAddr)
	attr(dwAtHighPC, dwFormAddr)
	attr(dwAtFrameBase, dwFormExprLoc)
	attr(0, 0)

	// abbrev 4: DW_TAG_base_type
	b = appendULEB(b, abbrevBaseType)
	b = appendULEB(b, dwTagBaseType)
	b = append(b, dwChildrenNo)
	attr(dwAtName, dwFormString)
	attr(dwAtByteSize, dwFormData1)
	attr(dwAtEncoding, dwFormData1)
	attr(0, 0)

	// abbrev 5 / 6: DW_TAG_formal_parameter / DW_TAG_variable
	for _, code := range []uint64{abbrevFormalParam, abbrevVariable} {
		b = appendULEB(b, code)
		if code == abbrevFormalParam {
			b = appendULEB(b, dwTagFormalParameter)
		} else {
			b = appendULEB(b, dwTagVariable)
		}
		b = append(b, dwChildrenNo)
		attr(dwAtName, dwFormString)
		attr(dwAtType, dwFormRef4)
		attr(dwAtLocation, dwFormExprLoc)
		attr(0, 0)
	}

	b = appendULEB(b, 0) // end of abbreviation table
	return b
}

// buildDebugInfo encodes the .debug_info compilation unit: the CU DIE spanning
// [textLo, textHi), the base-type DIEs referenced by any variables, then one
// subprogram DIE per symbol — carrying formal_parameter / variable child DIEs
// (from funcVars, keyed by symbol name) with DW_OP_fbreg locations relative to
// DW_AT_frame_base = the frame register (fbReg; DWARF reg 6 = x86 rbp, 29 =
// arm64 x29). When hasLines, the CU adds DW_AT_stmt_list.
func buildDebugInfo(syms []Sym, textLo, textHi uint64, name, compDir string, hasLines bool, funcVars map[string][]LocalVar, fbReg byte) []byte {
	cstr := func(b []byte, s string) []byte {
		b = append(b, s...)
		return append(b, 0)
	}
	// A DIE's DW_FORM_ref4 offset is measured from the start of the CU header;
	// the header is 11 bytes (unit_length 4 + version 2 + abbrev_off 4 +
	// address_size 1), so position p within `die` is CU offset 11+p.
	const cuHeaderLen = 11

	var die []byte
	// CU DIE (abbrev 1). DW_AT_name + DW_AT_comp_dir let a debugger locate
	// the source: gdb resolves a relative name against comp_dir.
	die = appendULEB(die, abbrevCompileUnit)
	die = cstr(die, "fern")    // DW_AT_producer
	die = le16(die, dwLangC99) // DW_AT_language
	die = cstr(die, name)      // DW_AT_name (source path as compiled)
	die = cstr(die, compDir)   // DW_AT_comp_dir (compilation directory)
	die = le64(die, textLo)    // DW_AT_low_pc
	die = le64(die, textHi)    // DW_AT_high_pc
	if hasLines {
		die = le32(die, 0) // DW_AT_stmt_list → .debug_line offset 0
	}

	// Emit one base_type DIE per distinct scalar type any variable uses, and
	// record each type's CU-relative offset for the DW_AT_type refs below.
	typeOff := map[string]uint32{}
	for _, s := range syms {
		for _, v := range funcVars[s.Name] {
			if _, ok := typeOff[v.TypeKey]; ok {
				continue
			}
			bt, ok := scalarBaseType[v.TypeKey]
			if !ok {
				continue
			}
			typeOff[v.TypeKey] = uint32(cuHeaderLen + len(die))
			die = appendULEB(die, abbrevBaseType)
			die = cstr(die, v.TypeKey) // DW_AT_name
			die = append(die, bt.size) // DW_AT_byte_size
			die = append(die, bt.enc)  // DW_AT_encoding
		}
	}

	// Subprogram DIEs, children of the CU. A function with scalar variables
	// uses abbrev 3 (frame base + parameter/variable children); others use the
	// bare abbrev 2.
	for _, s := range syms {
		vars := funcVars[s.Name]
		emittable := vars[:0]
		for _, v := range vars {
			if _, ok := typeOff[v.TypeKey]; ok {
				emittable = append(emittable, v)
			}
		}
		if len(emittable) == 0 {
			die = appendULEB(die, abbrevSubprogram)
			die = cstr(die, s.Name)
			die = le64(die, s.Value)
			die = le64(die, s.Value+s.Size)
			continue
		}
		die = appendULEB(die, abbrevSubprogramVars)
		die = cstr(die, s.Name)
		die = le64(die, s.Value)
		die = le64(die, s.Value+s.Size)
		// DW_AT_frame_base = exprloc [DW_OP_reg<fbReg>].
		die = appendULEB(die, 1)
		die = append(die, dwOpRegBase+fbReg)
		for _, v := range emittable {
			if v.IsParam {
				die = appendULEB(die, abbrevFormalParam)
			} else {
				die = appendULEB(die, abbrevVariable)
			}
			die = cstr(die, v.Name)
			die = le32(die, typeOff[v.TypeKey]) // DW_AT_type (ref4)
			// DW_AT_location = exprloc [DW_OP_fbreg <sleb offset>].
			loc := appendSLEB([]byte{dwOpFbreg}, int64(v.Offset))
			die = appendULEB(die, uint64(len(loc)))
			die = append(die, loc...)
		}
		die = appendULEB(die, 0) // end this subprogram's children
	}
	die = appendULEB(die, 0) // null DIE terminates the CU's children

	// CU header (DWARF v4): unit_length is everything after the length field.
	var body []byte
	body = le16(body, 4)   // version
	body = le32(body, 0)   // debug_abbrev_offset
	body = append(body, 8) // address_size (64-bit)
	body = append(body, die...)

	out := le32(nil, uint32(len(body))) // unit_length
	return append(out, body...)
}
