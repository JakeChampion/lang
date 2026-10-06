package e2eharness

import "strings"

// CommByteCase describes inputs that must retain byte ordering and spelling.
type CommByteCase struct {
	Name, A, B, Stdin string
	Args              []string
	Shared            bool
	Exit              int
}

// CommByteCases covers both record delimiters, all byte values, chunk-spanning
// records and retained ordering history. Args exclude the two file operands.
func CommByteCases() []CommByteCase {
	var cases []CommByteCase
	for _, term := range []byte{'\n', 0} {
		var a, b strings.Builder
		for i := 0; i < 256; i++ {
			if byte(i) == term {
				continue
			}
			record := string([]byte{byte(i), 0xff, term})
			a.WriteString(record)
			if i%3 == 0 {
				b.WriteString(record)
			} else if i%3 == 1 {
				b.WriteString(string([]byte{byte(i), 0xfe, term}))
			}
		}
		name := "raw LF"
		args := []string{"--total"}
		if term == 0 {
			name = "raw NUL"
			args = append(args, "-z")
		}
		cases = append(cases,
			CommByteCase{Name: name, A: a.String(), B: b.String(), Args: args},
			CommByteCase{Name: name + " missing terminator", A: strings.TrimSuffix(a.String(), string(term)), B: a.String(), Args: args},
			CommByteCase{Name: name + " suppressed", A: a.String(), B: b.String(), Args: append(append([]string{}, args...), "-123")},
		)
	}
	long := strings.Repeat("\xff\xc0\x80\x00x", 50000)
	cases = append(cases,
		CommByteCase{Name: "raw long shared prefix", A: long + "a\n" + long + "b\n", B: long + "a\n" + long + "c"},
		CommByteCase{Name: "raw shared stdin", Stdin: "\x80\n\x80\n\xff\n\xff", Shared: true},
		CommByteCase{Name: "raw late disorder", A: "\xff\n\x80\n", B: "\xff\n", Exit: 1},
		CommByteCase{Name: "raw empty", A: "", B: ""},
	)
	return cases
}
