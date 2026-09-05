package x86_64

import (
	"fmt"
	"runtime"
	"strings"
)

// ParseProgram decodes the Intel-syntax program text into an assembler
// holding instructions, labels and unresolved fixups — everything that does
// not depend on where the image will be loaded. The BytesProgram* methods
// then lay it out at a given address.
//
// The split matters because some outputs can only be computed AFTER the
// layout: .eh_frame declares pcrel FDE pointers, so rendering it needs its own
// load address, which follows from len(text). Mirrors arm64.ParseProgram.
//
// Reading a line — its comment, its labels, and for an instruction the
// operand decode — depends on nothing but the line, so the lines are read
// in chunks on one worker per CPU; feeding them to the assembler, which is
// where section state and the text offset live, walks the chunks in order.
func ParseProgram(src string) (*Assembler, error) {
	return parseProgram(src, runtime.GOMAXPROCS(0))
}

// parsedLine is one source line after the part of the parse that needs no
// assembler state.
type parsedLine struct {
	raw    string // the line as written, for the error message
	labels []string
	text   string // what follows the labels, trimmed; "" for a blank line
	inst   Inst   // the decoded instruction when text is not a directive
	err    error  // ParseInst's failure, raised when the walk reaches the line
}

// parseChunkLines is the line-local half of the parse.
func parseChunkLines(lines []string) []parsedLine {
	out := make([]parsedLine, len(lines))
	for i, raw := range lines {
		p := &out[i]
		p.raw = raw
		line := stripComment(raw)
		for {
			label, rest, ok := splitLabel(line)
			if !ok {
				break
			}
			p.labels = append(p.labels, label)
			line = strings.TrimSpace(rest)
		}
		p.text = strings.TrimSpace(line)
		if p.text != "" && !strings.HasPrefix(p.text, ".") {
			p.inst, p.err = ParseInst(p.text)
		}
	}
	return out
}

// parseChunkSize is how many lines one worker reads at a time: large enough
// that the hand-off per chunk is noise against the decode, small enough
// that the ordered walk starts before the last chunk is read.
const parseChunkSize = 4096

func parseProgram(src string, jobs int) (*Assembler, error) {
	lines := strings.Split(src, "\n")
	a := NewProgram()
	feed := func(base int, parsed []parsedLine) error {
		for i := range parsed {
			p := &parsed[i]
			for _, label := range p.labels {
				a.Label(label)
			}
			if p.text == "" {
				continue
			}
			err := p.err
			if err == nil {
				if strings.HasPrefix(p.text, ".") {
					err = a.Directive(p.text)
				} else {
					err = a.Inst(p.inst)
				}
			}
			if err != nil {
				return fmt.Errorf("line %d: %q: %w", base+i+1, strings.TrimSpace(p.raw), err)
			}
		}
		return nil
	}
	if jobs <= 1 || len(lines) <= parseChunkSize {
		return a, feed(0, parseChunkLines(lines))
	}
	// Chunks are read on goroutines whose results queue in source order;
	// the queue's capacity bounds how far the readers run ahead of the
	// walk, and so the memory the decoded operands hold at once.
	type chunk struct {
		base   int
		parsed []parsedLine
	}
	queue := make(chan chan chunk, 2*jobs)
	go func() {
		for base := 0; base < len(lines); base += parseChunkSize {
			hi := base + parseChunkSize
			if hi > len(lines) {
				hi = len(lines)
			}
			done := make(chan chunk, 1)
			queue <- done
			go func(base int, part []string) {
				done <- chunk{base, parseChunkLines(part)}
			}(base, lines[base:hi])
		}
		close(queue)
	}()
	var err error
	for done := range queue {
		c := <-done
		if err == nil {
			err = feed(c.base, c.parsed)
		}
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}
