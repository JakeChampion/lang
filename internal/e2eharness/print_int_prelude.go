package e2eharness

import (
	"regexp"
	"strings"
)

// fernPrinters are checked-Fern definitions of the number printers and the
// stdin integer reader that test programs call. Neither checker knows these
// names, so a program that uses one has to define it. The printers write
// decimal text with no newline and allocate nothing; read_int parses an
// optional '-' and the leading decimal digits of one stdin line, 0 at EOF.
var fernPrinters = []struct {
	name string
	def  string
}{
	{"print_int", `
function print_int(n: i32): i32 {
    if (n < 0) {
        putchar(45);
        if (n < 0 - 9) { print_int(0 - n / 10); }
        putchar(48 - n % 10);
        return 0;
    }
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
`},
	{"print_i64", `
function print_i64(n: i64): i32 {
    if (n < 0i64) {
        putchar(45);
        if (n < 0i64 - 9i64) { print_i64(0i64 - n / 10i64); }
        putchar(48 - ((n % 10i64) as i32));
        return 0;
    }
    if (n > 9i64) { print_i64(n / 10i64); }
    putchar(48 + ((n % 10i64) as i32));
    return 0;
}
`},
	{"read_int", `
function read_int(): i32 {
    var line: string = "";
    match (read_line()) { Some(s) => { line = s; }, None => { return 0; } }
    var i: i32 = 0;
    var neg: boolean = false;
    if (line.len() > 0 && line[0] == (45 as u8)) { neg = true; i = 1; }
    var acc: i32 = 0;
    while (i < line.len() && line[i] >= (48 as u8) && line[i] <= (57 as u8)) {
        acc = acc * 10 + ((line[i] - (48 as u8)) as i32);
        i = i + 1;
    }
    if (neg) { return 0 - acc; }
    return acc;
}
`},
}

var fernPrinterUse = map[string]*regexp.Regexp{}

func init() {
	for _, p := range fernPrinters {
		fernPrinterUse[p.name] = regexp.MustCompile(`\b` + p.name + `\(`)
	}
}

// WithPrintInt appends to src the definition of each of print_int, print_i64
// and read_int that src calls without defining itself.
func WithPrintInt(src string) string {
	for _, p := range fernPrinters {
		if fernPrinterUse[p.name].MatchString(src) && !strings.Contains(src, "function "+p.name+"(") {
			src += p.def
		}
	}
	return src
}
