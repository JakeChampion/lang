package x86_64ssa

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The trampoline behind ssaBumpAlloc must be called at a 16-aligned rsp
// (freelist.go). A helper is entered 8 past alignment, so between its label
// and every trampoline call the pushes and rsp adjustments on that path
// must net to an odd number of 8-byte slots. This walks each helper's text
// as a stack-pointer interpreter: pushes, pops and constant rsp arithmetic
// move the offset, jumps and conditional jumps fork the walk, a return
// ends it, and any trampoline call reached at the wrong parity is named.
func TestEveryHelperCallsTheTrampolineAligned(t *testing.T) {
	var names []string
	for name := range runtimeHelperEmitters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var b strings.Builder
		runtimeHelperEmitters[name](func(format string, args ...any) {
			b.WriteString(fmt.Sprintf(format, args...) + "\n")
		})
		for _, site := range misalignedTrampolineCalls(b.String()) {
			t.Errorf("%s calls %s with rsp %d past 16-alignment at line %d: %s", name, allocPresSym, site.off, site.line, site.text)
		}
	}
}

type misalignedSite struct {
	line int
	off  int
	text string
}

// misalignedTrampolineCalls interprets rsp over the helper text (entered 8
// past alignment) and returns the trampoline calls reached at a misaligned
// offset. A path is walked once per (line, offset) pair, so loops end.
func misalignedTrampolineCalls(text string) []misalignedSite {
	lines := strings.Split(text, "\n")
	labels := map[string]int{}
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasSuffix(l, ":") && !strings.Contains(l, " ") {
			labels[strings.TrimSuffix(l, ":")] = i
		}
	}
	type state struct{ line, off int }
	seen := map[state]bool{}
	var out []misalignedSite
	var walk func(line, off int)
	walk = func(line, off int) {
		for ; line < len(lines); line++ {
			st := state{line, off}
			if seen[st] {
				return
			}
			seen[st] = true
			l := strings.TrimSpace(lines[line])
			if l == "" || strings.HasSuffix(l, ":") || strings.HasPrefix(l, "#") || strings.HasPrefix(l, ".") {
				continue
			}
			if i := strings.Index(l, "//"); i >= 0 {
				l = strings.TrimSpace(l[:i])
			}
			op, rest, _ := strings.Cut(l, " ")
			rest = strings.TrimSpace(rest)
			switch {
			case op == "push":
				off += 8
			case op == "pop":
				off -= 8
			case (op == "sub" || op == "add") && strings.HasPrefix(rest, "rsp,"):
				n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(rest, "rsp,")))
				if err != nil {
					out = append(out, misalignedSite{line + 1, -1, l + " (an rsp adjustment this walk cannot read)"})
					return
				}
				if op == "sub" {
					off += n
				} else {
					off -= n
				}
			case strings.Contains(rest, "rsp") && (op == "mov" || op == "lea") && strings.HasPrefix(rest, "rsp"):
				out = append(out, misalignedSite{line + 1, -1, l + " (an rsp write this walk cannot read)"})
				return
			case op == "call":
				if rest == allocPresSym && (8+off)%16 != 0 {
					out = append(out, misalignedSite{line + 1, (8 + off) % 16, l})
				}
			case op == "ret":
				return
			case op == "jmp":
				if to, ok := labels[rest]; ok {
					walk(to, off)
				}
				return
			case strings.HasPrefix(op, "j"):
				if to, ok := labels[rest]; ok {
					walk(to, off)
				}
			}
		}
	}
	walk(0, 0)
	return out
}
