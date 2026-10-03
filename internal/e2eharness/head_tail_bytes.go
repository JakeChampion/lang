package e2eharness

import "strings"

type HeadTailByteCase struct {
	Name, Input string
	Args        []string
	File        bool
}

// HeadTailByteCases crosses both read sizes with malformed bytes and tests
// record and byte boundaries on seekable inputs and streams.
func HeadTailByteCases(utility string) []HeadTailByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	data := strings.Repeat(string(all), 513) + "\xff\xc0\x80last"
	long := strings.Repeat("\xff\xc0\x80x", 20000) + "\nlast\x00\xff"
	boundary := strings.Repeat("\xff", 65535) + "\n" + strings.Repeat("\x80", 8191) + "\n\xc0tail"
	cases := []HeadTailByteCase{
		{Name: "raw lines", Input: data, Args: []string{"-n", "3"}},
		{Name: "raw bytes", Input: data, Args: []string{"-c", "257"}},
		{Name: "raw retained blocks", Input: strings.Repeat(data, 3), Args: []string{"-c", "100003"}},
		{Name: "raw retained lines", Input: strings.Repeat(data, 3), Args: []string{"-n", "400"}},
		{Name: "raw wide count", Input: data, Args: []string{"-c", "2147483648"}},
		{Name: "raw NUL records", Input: data, Args: []string{"-z", "-n", "3"}},
		{Name: "raw long line", Input: long, Args: []string{"-n", "1"}},
		{Name: "raw block boundary", Input: boundary, Args: []string{"-n", "1"}},
		{Name: "raw zero count", Input: data, Args: []string{"-c", "0"}},
		{Name: "raw empty", Args: []string{"-n", "2"}},
	}
	if utility == "head" {
		cases = append(cases,
			HeadTailByteCase{Name: "raw elided lines", Input: data, Args: []string{"-n", "-3"}},
			HeadTailByteCase{Name: "raw elided bytes", Input: data, Args: []string{"-c", "-257"}},
			HeadTailByteCase{Name: "raw elided blocks", Input: strings.Repeat(data, 3), Args: []string{"-c", "-100003"}},
			HeadTailByteCase{Name: "raw elided wide count", Input: data, Args: []string{"-c", "-2147483648"}},
			HeadTailByteCase{Name: "raw elided retained lines", Input: strings.Repeat(data, 3), Args: []string{"-n", "-400"}},
			HeadTailByteCase{Name: "raw elided NUL", Input: data, Args: []string{"-z", "-n", "-2"}},
			HeadTailByteCase{Name: "raw elided long line", Input: long, Args: []string{"-n", "-1"}},
			HeadTailByteCase{Name: "raw elided zero", Input: data, Args: []string{"-n", "-0"}},
		)
	} else {
		cases = append(cases,
			HeadTailByteCase{Name: "raw from line", Input: data, Args: []string{"-n", "+3"}},
			HeadTailByteCase{Name: "raw from byte", Input: data, Args: []string{"-c", "+257"}},
			HeadTailByteCase{Name: "raw from NUL", Input: data, Args: []string{"-z", "-n", "+2"}},
		)
	}
	stream := append([]HeadTailByteCase(nil), cases...)
	for _, tc := range stream {
		tc.Name += " file"
		tc.File = true
		cases = append(cases, tc)
	}
	return cases
}
