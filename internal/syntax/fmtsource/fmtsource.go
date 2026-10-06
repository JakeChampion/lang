// Package fmtsource formats Fern source text: what `fern -fmt` writes for a
// .fern file. It sits outside internal/syntax/printer because the parser's tests
// import the printer.
package fmtsource

import (
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

// Format parses src and returns its formatted text.
func Format(src string) (string, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return "", err
	}
	return printer.Format(prog), nil
}
