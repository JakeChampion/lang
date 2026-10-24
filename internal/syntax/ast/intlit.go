package ast

import (
	"fmt"
	"strconv"
)

func IntLitOutOfRange(lit *NumberLit, negated bool, t NumberType) string {
	magnitude := uint64(lit.Value)
	if !lit.ExceedsI64 && lit.Value < 0 {
		magnitude = uint64(-lit.Value)
		negated = !negated
	}
	if magnitude == 0 && !lit.ExceedsU64 {
		negated = false
	}
	text := lit.Raw
	if text == "" {
		text = strconv.FormatUint(magnitude, 10)
	}
	if negated {
		text = "-" + text
	}
	msg := fmt.Sprintf("literal %s does not fit in %s", text, t)
	if lit.ExceedsU64 {
		return msg
	}
	if t.IsSigned() {
		var limit uint64
		switch t.NormalWidth() {
		case 32:
			limit = 1<<31 - 1
		case 64:
			limit = 1<<63 - 1
		default:
			return ""
		}
		if negated {
			limit++
		}
		if magnitude > limit {
			return msg
		}
		return ""
	}
	if negated {
		return msg + ": unsigned types have no negative values"
	}
	var max uint64
	switch t.NormalWidth() {
	case 8:
		max = 1<<8 - 1
	case 32:
		max = 1<<32 - 1
	default:
		return ""
	}
	if magnitude > max {
		return msg
	}
	return ""
}
