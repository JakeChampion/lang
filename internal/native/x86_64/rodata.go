package x86_64

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/native/gasstr"
)

func (a *Assembler) appendRodata(b []byte) { a.rodata = append(a.rodata, b...) }

func (a *Assembler) alignRodata(n int) {
	if n <= 1 {
		return
	}
	for len(a.rodata)%n != 0 {
		a.rodata = append(a.rodata, 0)
	}
}

// appendRodataDirective materialises a .rodata data directive into the
// data blob. Strings store a 4-byte length prefix (emitted by the code
// generator as a separate .4byte) immediately before the data label, so
// the layout here just has to be contiguous and order-preserving.
func (a *Assembler) appendRodataDirective(d, rest string) error {
	switch d {
	case ".byte":
		return a.emitInts(rest, 1)
	case ".2byte", ".hword", ".short", ".half", ".value":
		return a.emitInts(rest, 2)
	case ".4byte", ".word", ".long", ".int":
		return a.emitInts(rest, 4)
	case ".8byte", ".xword", ".quad", ".dword":
		return a.emitInts(rest, 8)
	case ".double", ".dc.d":
		return a.emitFloats(rest, 8)
	case ".float", ".single", ".dc.s":
		return a.emitFloats(rest, 4)
	case ".ascii", ".asciz", ".string":
		s, err := gasstr.Unquote(strings.TrimSpace(rest))
		if err != nil {
			return fmt.Errorf("bad string literal %q: %v", rest, err)
		}
		a.appendRodata([]byte(s))
		if d != ".ascii" {
			a.appendRodata([]byte{0})
		}
		return nil
	case ".space", ".skip":
		n, err := strconv.Atoi(strings.Fields(rest)[0])
		if err != nil {
			return fmt.Errorf("bad .space/.skip size %q", rest)
		}
		a.appendRodata(make([]byte, n))
		return nil
	case ".align", ".balign":
		// On x86 GAS these take a byte count (unlike arm64's power-of-two).
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return fmt.Errorf("bad alignment %q", rest)
		}
		a.alignRodata(n)
		return nil
	case ".p2align":
		n, err := strconv.Atoi(strings.Fields(rest)[0])
		if err != nil {
			return fmt.Errorf("bad .p2align %q", rest)
		}
		a.alignRodata(1 << n)
		return nil
	case ".globl", ".global", ".type", ".size", ".intel_syntax":
		return nil
	default:
		return fmt.Errorf("unsupported .rodata directive %q", d)
	}
}

func (a *Assembler) emitInts(rest string, width int) error {
	for _, tok := range strings.Split(rest, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		v, err := strconv.ParseInt(tok, 0, 64)
		if err != nil {
			u, uerr := strconv.ParseUint(tok, 0, 64)
			if uerr != nil {
				// A ".quad <symbol>" slot: emit the symbol's absolute
				// 8-byte address (function-/data-pointer tables).
				if width == 8 && isLabelName(tok) {
					a.quadSyms = append(a.quadSyms, quadSymFixup{at: len(a.rodata), sym: tok})
					a.appendRodata(make([]byte, 8))
					continue
				}
				return fmt.Errorf("bad integer %q", tok)
			}
			v = int64(u)
		}
		uv := uint64(v)
		b := make([]byte, width)
		for i := 0; i < width; i++ {
			b[i] = byte(uv >> (8 * i))
		}
		a.appendRodata(b)
	}
	return nil
}

func (a *Assembler) emitFloats(rest string, width int) error {
	for _, tok := range strings.Split(rest, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		f, err := strconv.ParseFloat(tok, 64)
		if err != nil {
			return fmt.Errorf("bad float %q", tok)
		}
		var uv uint64
		if width == 4 {
			uv = uint64(math.Float32bits(float32(f)))
		} else {
			uv = math.Float64bits(f)
		}
		b := make([]byte, width)
		for i := 0; i < width; i++ {
			b[i] = byte(uv >> (8 * i))
		}
		a.appendRodata(b)
	}
	return nil
}
