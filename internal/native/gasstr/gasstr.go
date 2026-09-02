// Package gasstr decodes the GNU-as string literals the code generators emit
// into .ascii / .asciz / .string directives.
//
// It exists because strconv.Unquote is the wrong tool for this and fails
// SILENTLY. A Go quoted string is UTF-8 text, so Unquote decodes each byte it
// reads as a rune and re-encodes it: a lone 0x80 is invalid UTF-8, decodes to
// utf8.RuneError, and comes back out as U+FFFD's three bytes (ef bf bd) — no
// error returned, just three wrong bytes where one right one belonged, and a
// literal whose length no longer matches the .4byte length the generator
// emitted alongside it.
//
// GNU as has no such notion: a .asciz operand is a byte string, and both
// emitters (x86_64's escapeForGAS and its arm64 twin) deliberately pass bytes
// >= 0x80 through raw. So assembling the SAME text in-process and via gcc
// produced different data — a divergence invisible in any program whose
// literals are pure ASCII, and one that corrupts every binary blob carried in
// \xNN escapes (the self-host wasm component framing in watbin.fern is exactly
// that, which is how this surfaced).
//
// Decoding is therefore byte-wise, never rune-wise.
//
// The DWARF `.file` / `.loc` directives live here too: both assemblers record
// them identically, and a row type each side defined for itself is the
// drift class #7903 exists to kill.
package gasstr

import (
	"fmt"
	"strconv"
)

// Unquote decodes one double-quoted GNU-as string operand into its bytes,
// resolving the escapes GAS recognises and passing every other byte —
// including any byte >= 0x80 — through unchanged.
func Unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("not a quoted string: %q", s)
	}
	body := s[1 : len(s)-1]
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("trailing backslash in %q", s)
		}
		switch e := body[i]; {
		case e == 'n':
			out = append(out, '\n')
		case e == 't':
			out = append(out, '\t')
		case e == 'r':
			out = append(out, '\r')
		case e == 'b':
			out = append(out, '\b')
		case e == 'f':
			out = append(out, '\f')
		case e == 'v':
			out = append(out, '\v')
		case e == 'a':
			out = append(out, 7)
		case e == '\\' || e == '"' || e == '\'':
			out = append(out, e)
		case e >= '0' && e <= '7':
			// Octal, one to three digits — GAS truncates to a byte.
			v := 0
			n := 0
			for n < 3 && i < len(body) && body[i] >= '0' && body[i] <= '7' {
				v = v*8 + int(body[i]-'0')
				i++
				n++
			}
			i-- // the loop's own i++ steps past the last digit
			out = append(out, byte(v))
		case e == 'x':
			// Hex, one or more digits — GAS keeps the low byte.
			v := 0
			n := 0
			for i+1 < len(body) && isHex(body[i+1]) {
				v = v*16 + hexVal(body[i+1])
				i++
				n++
			}
			if n == 0 {
				return "", fmt.Errorf("\\x with no hex digits in %q", s)
			}
			out = append(out, byte(v))
		default:
			return "", fmt.Errorf("unknown escape \\%c in %q", e, s)
		}
	}
	return string(out), nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

// LineRow is one DWARF .debug_line row as a `.loc` directive records it: the
// source position active at a byte offset within .text. The offset is the
// next instruction's, since `.loc` emits no bytes; the image writer converts
// it to an absolute address. File indexes the `.file N "path"` table.
//
// Both assemblers record exactly this, so it is shared rather than written
// twice; the ELF writer has its own row type keyed by absolute address.
type LineRow struct {
	Offset        int
	File          int
	Line          int
	Col           int
	PrologueEnd   bool
	EpilogueBegin bool
	// IsStmt is the state machine's is_stmt register as of this row.
	IsStmt bool
}

// FileDirective parses the operands of a `.file` directive into the table
// files, returning the (possibly newly allocated) table. Only the numbered
// DWARF form `.file N "path"` defines an entry; the bare `.file "path"` form
// is the ELF source-name symbol and is accepted without effect.
func FileDirective(files map[int]string, ops []string) (map[int]string, error) {
	if len(ops) == 1 {
		return files, nil
	}
	if len(ops) != 2 {
		return files, fmt.Errorf(".file: want `.file N \"path\"`, got %d operands", len(ops))
	}
	n, err := strconv.Atoi(ops[0])
	if err != nil || n < 1 {
		return files, fmt.Errorf(".file: bad file number %q", ops[0])
	}
	path, err := Unquote(ops[1])
	if err != nil {
		return files, fmt.Errorf(".file %d: %v", n, err)
	}
	if files == nil {
		files = map[int]string{}
	}
	if prev, dup := files[n]; dup && prev != path {
		return files, fmt.Errorf(".file %d: already %q, now %q", n, prev, path)
	}
	files[n] = path
	return files, nil
}

// LocDirective parses the operands of `.loc file line [column] [options]`
// into the row for the code at offset off. The options gas defines and this
// accepts are prologue_end, epilogue_begin and `is_stmt N`; anything else
// (isa, discriminator, basic_block, a view) is refused rather than dropped,
// since a consumer would then read a table that silently disagrees with the
// source.
//
// is_stmt is a register of the line-number state machine, not a per-row
// flag: `is_stmt 0` stays in force for every later `.loc` until an
// `is_stmt 1`, which is why the previous row's value is an input. The other
// two flags apply to their own row only.
func LocDirective(ops []string, off int, isStmt bool) (LineRow, error) {
	row := LineRow{Offset: off, IsStmt: isStmt}
	if len(ops) < 2 {
		return row, fmt.Errorf(".loc: want `.loc file line [column]`")
	}
	var err error
	if row.File, err = strconv.Atoi(ops[0]); err != nil || row.File < 1 {
		return row, fmt.Errorf(".loc: bad file number %q", ops[0])
	}
	if row.Line, err = strconv.Atoi(ops[1]); err != nil || row.Line < 0 {
		return row, fmt.Errorf(".loc: bad line %q", ops[1])
	}
	rest := ops[2:]
	if len(rest) > 0 {
		if col, err := strconv.Atoi(rest[0]); err == nil {
			if col < 0 {
				return row, fmt.Errorf(".loc: bad column %q", rest[0])
			}
			row.Col = col
			rest = rest[1:]
		}
	}
	for len(rest) > 0 {
		switch rest[0] {
		case "prologue_end":
			row.PrologueEnd = true
			rest = rest[1:]
		case "epilogue_begin":
			row.EpilogueBegin = true
			rest = rest[1:]
		case "is_stmt":
			if len(rest) < 2 || (rest[1] != "0" && rest[1] != "1") {
				return row, fmt.Errorf(".loc: is_stmt wants 0 or 1")
			}
			row.IsStmt = rest[1] == "1"
			rest = rest[2:]
		default:
			return row, fmt.Errorf(".loc: unsupported option %q", rest[0])
		}
	}
	return row, nil
}
