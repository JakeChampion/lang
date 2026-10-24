package fmtsource

import (
	"strings"

	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
	"github.com/jakechampion/lang/internal/tools/literate"
)

func Format(src string) (string, error) {
	if strings.Contains(src, "```") && literate.Parse(src).IsProgram() {
		return FormatLiterate(src), nil
	}
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	return printer.Format(prog), nil
}

func FormatLiterate(src string) string {
	return literate.Parse(src).FormatCode(formatChunkBody)
}

func formatChunkBody(code string) (string, bool) {
	if strings.TrimSpace(code) == "" {
		return "", false
	}
	if prog, err := parser.Parse(code); err == nil {
		return strings.TrimRight(printer.Format(prog), "\n"), true
	}
	if prog, err := parser.Parse(wrapOpen + code + "\n}\n"); err == nil {
		return unwrapFormattedBody(printer.Format(prog)), true
	}
	return "", false
}

const wrapOpen = "function __fern_fmt_wrap__() {\n"

func unwrapFormattedBody(formatted string) string {
	lines := strings.Split(strings.TrimRight(formatted, "\n"), "\n")
	if len(lines) < 2 {
		return ""
	}
	inner := lines[1 : len(lines)-1]
	for i, ln := range inner {
		inner[i] = strings.TrimPrefix(ln, formatIndentUnit)
	}
	return strings.Join(inner, "\n")
}

const formatIndentUnit = "  "
