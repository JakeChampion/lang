package diag

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type Positioned interface {
	error
	Position() ast.Position
}

type Spanned interface {
	Positioned
	Length() int
}

type Hinted interface {
	error
	Hint() string
}

type Filed interface {
	error
	File() string
}

type Coded interface {
	error
	Code() string
}

type LabelKind int

const (
	LabelPrimary LabelKind = iota
	LabelSecondary
	LabelHelp
)

type Label struct {
	Pos     ast.Position
	Length  int
	Message string
	Kind    LabelKind
}

type Suggestion struct {
	Pos         ast.Position
	Length      int
	Replacement string
	Title       string
}

type Suggested interface {
	error
	Suggestion() *Suggestion
}

type Labeled interface {
	error
	Labels() []Label
}

func WithFile(err error, filename string) error {
	if err == nil {
		return nil
	}
	if es, ok := err.(Errors); ok {
		for _, e := range es {
			stampFile(e, filename)
		}
		return es
	}
	stampFile(err, filename)
	return err
}

type FileSetter interface {
	error
	SetFile(filename string)
}

func stampFile(err error, filename string) {
	if s, ok := err.(FileSetter); ok {
		s.SetFile(filename)
	}
}

type Errors []error

func (es Errors) SortedByPosition(entry string) Errors {
	out := append(Errors(nil), es...)
	key := func(e error) (string, int, int) {
		file := ""
		if f, ok := e.(Filed); ok && f.File() != entry {
			file = f.File()
		}
		if p, ok := e.(Positioned); ok {
			return file, p.Position().Line, p.Position().Col
		}
		return file, 0, 0
	}
	sort.SliceStable(out, func(i, j int) bool {
		fi, li, ci := key(out[i])
		fj, lj, cj := key(out[j])
		if fi != fj {
			return fi < fj
		}
		if li != lj {
			return li < lj
		}
		return ci < cj
	})
	return out
}

func (es Errors) Error() string {
	switch len(es) {
	case 0:
		return ""
	case 1:
		return es[0].Error()
	}
	var b strings.Builder
	for i, e := range es {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

func (es Errors) As(target any) bool {
	for _, e := range es {
		if as, ok := e.(interface{ As(any) bool }); ok && as.As(target) {
			return true
		}
	}
	return false
}

func Format(filename, src string, err error) string {
	return format(filename, src, nil, err)
}

func FormatRemapped(filename, displaySrc string, remap func(ast.Position) ast.Position, err error) string {
	return format(filename, displaySrc, remap, err)
}

func format(filename, src string, remap func(ast.Position) ast.Position, err error) string {
	if err == nil {
		return ""
	}
	if es, ok := err.(Errors); ok {
		var b strings.Builder
		for i, e := range es {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(format(filename, src, remap, e))
		}
		return b.String()
	}

	pe, ok := err.(Positioned)
	if !ok {
		return diagnosticText(err.Error())
	}
	pos := pe.Position()
	if remap != nil {
		pos = remap(pos)
	}
	line := pickLine(src, pos.Line)
	if line == "" && pos.Line == 0 {
		if cd, ok := err.(Coded); ok {
			if code := cd.Code(); code != "" {
				return diagnosticText(paint(ansiRedBld, "error["+code+"]") + ": " + stripPrefix(pe.Error()))
			}
		}
		return diagnosticText(err.Error())
	}

	prefix := "error"
	if cd, ok := err.(Coded); ok {
		if code := cd.Code(); code != "" {
			prefix = "error[" + code + "]"
		}
	}
	header := fmt.Sprintf("%d:%d: %s: %s", pos.Line, pos.Col, paint(ansiRedBld, prefix), stripPrefix(pe.Error()))
	if filename != "" {
		header = filename + ":" + header
	}
	if terse {
		return diagnosticText(header)
	}

	pad := strings.Repeat(" ", clamp(pos.Col-1, 0, len(line)))
	mark := "^"
	if sp, ok := err.(Spanned); ok && sp.Length() > 1 {
		room := len(line) - (pos.Col - 1) - 1
		if room < 0 {
			room = 0
		}
		span := sp.Length()
		if span-1 > room {
			span = room + 1
		}
		mark = "^" + strings.Repeat("~", span-1)
	}

	coloredMark := paint(ansiRed, mark)
	var out, notePrefix string
	if enableColor {
		num := fmt.Sprintf("%d", pos.Line)
		blank := strings.Repeat(" ", len(num))
		sep := paint(ansiBlue, boxVert())
		out = fmt.Sprintf("%s\n %s %s %s\n %s %s %s%s", header, num, sep, line, blank, sep, pad, coloredMark)
		notePrefix = " " + blank + " " + sep + " "
	} else {
		out = fmt.Sprintf("%s\n    %s\n    %s%s", header, line, pad, coloredMark)
		notePrefix = "    "
	}
	if h, ok := err.(Hinted); ok {
		if hint := h.Hint(); hint != "" {
			out += "\n" + notePrefix + paint(ansiBlue, "note:") + " " + hint
		}
	}
	if sg, ok := err.(Suggested); ok {
		if fix := sg.Suggestion(); fix != nil && fix.Title != "" {
			out += "\n" + notePrefix + paint(ansiBlue, "help:") + " " + fix.Title
		}
	}
	if lb, ok := err.(Labeled); ok {
		labels := lb.Labels()
		for i, l := range labels {
			if i == 0 {
				continue
			}
			if remap != nil {
				l.Pos = remap(l.Pos)
			}
			out += "\n" + renderSecondaryLabel(filename, src, l)
		}
	}
	return out
}

func renderSecondaryLabel(filename, src string, l Label) string {
	word := "note:"
	markColor := ansiBlue
	if l.Kind == LabelHelp {
		word = "help:"
		markColor = ansiGreen
	}
	tag := paint(markColor, word) + " "
	line := pickLine(src, l.Pos.Line)
	header := fmt.Sprintf("    %d:%d: %s%s", l.Pos.Line, l.Pos.Col, tag, l.Message)
	if filename != "" {
		header = "    " + filename + ":" + fmt.Sprintf("%d:%d: %s%s", l.Pos.Line, l.Pos.Col, tag, l.Message)
	}
	if line == "" {
		return header
	}
	pad := strings.Repeat(" ", clamp(l.Pos.Col-1, 0, len(line)))
	span := l.Length
	if span < 1 {
		span = 1
	}
	room := len(line) - (l.Pos.Col - 1)
	if room < 0 {
		room = 0
	}
	if span > room {
		span = room
	}
	mark := strings.Repeat("-", span)
	if mark == "" {
		mark = "-"
	}
	if enableColor {
		num := fmt.Sprintf("%d", l.Pos.Line)
		blank := strings.Repeat(" ", len(num))
		sep := paint(ansiBlue, boxVert())
		return fmt.Sprintf("%s\n %s %s %s\n %s %s %s%s", header, num, sep, line, blank, sep, pad, paint(markColor, mark))
	}
	return fmt.Sprintf("%s\n        %s\n        %s%s", header, line, pad, paint(markColor, mark))
}

func stripPrefix(msg string) string {
	if i := strings.Index(msg, ": "); i >= 0 {
		head := msg[:i]
		if strings.Contains(head, " error at ") {
			return msg[i+2:]
		}
	}
	return msg
}

func pickLine(src string, n int) string {
	if n <= 0 {
		return ""
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if cur == n {
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src)
			} else {
				end += i
			}
			return strings.ReplaceAll(src[i:end], "\t", " ")
		}
		if src[i] == '\n' {
			cur++
			start = i + 1
		}
	}
	if cur == n {
		return strings.ReplaceAll(src[start:], "\t", " ")
	}
	return ""
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func Suggest(name string, candidates []string) string {
	if name == "" || len(candidates) == 0 {
		return ""
	}
	budget := 2
	if len(name) <= 3 {
		budget = 1
	}
	best := ""
	bestDist := budget + 1
	for _, c := range candidates {
		if c == name {
			continue
		}
		d := levenshtein(name, c)
		if d < bestDist {
			best = c
			bestDist = d
		}
	}
	if bestDist > budget {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
