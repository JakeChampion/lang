package e2eharness

import "strings"

// PasteByteCase uses file operands a, b and empty, or shared stdin (-).
type PasteByteCase struct {
	Name, A, B, Stdin string
	Args              []string
}

func PasteByteCases() []PasteByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	a := string(all) + "\n\xfftail"
	b := "\x80\n\xfe\n\xc0\x00\n"
	long := strings.Repeat("\xff\xc0\x80\x00x", 50000)
	return []PasteByteCase{
		{Name: "raw parallel", A: a, B: b, Args: []string{"a", "b"}},
		{Name: "raw serial", A: a, B: b, Args: []string{"-s", "a", "b"}},
		{Name: "raw NUL parallel", A: a, B: b, Args: []string{"-z", "a", "b"}},
		{Name: "raw NUL serial", A: a, B: b, Args: []string{"-zs", "a", "b"}},
		{Name: "raw UTF8 delimiters", A: a, B: b, Args: []string{"-d", "é界", "empty", "a", "empty", "b", "empty"}},
		{Name: "raw UTF8 serial delimiters", A: a, B: b, Args: []string{"-sd", "é界", "a", "b"}},
		{Name: "raw suppressed delimiters", A: a, B: b, Args: []string{"-d", "\\0é\\0", "empty", "a", "empty", "b", "empty"}},
		{Name: "raw long lines", A: long + "\n\xfe", B: "\x80\n" + long, Args: []string{"a", "b"}},
		{Name: "raw long serial", A: long + "\n\xfe", B: "\x80\n" + long, Args: []string{"-sd", "é", "a", "b"}},
		{Name: "raw shared stdin", Stdin: a + b, Args: []string{"-d", "é", "-", "-", "-"}},
		{Name: "raw shared serial stdin", Stdin: a + b, Args: []string{"-sd", "é", "-", "-"}},
		{Name: "raw empty columns", A: a, Args: []string{"-d", "é", "empty", "empty", "a", "empty", "empty"}},
	}
}
