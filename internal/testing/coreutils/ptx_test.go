package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ptxFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func init() {
	registerCorpus("ptx", ptxCases)
}

func ptxCases(t *testing.T) []invocation {
	dir := t.TempDir()
	in1 := ptxFile(t, dir, "in1", "the quick brown fox\njumps over the lazy dog\n")
	sent := ptxFile(t, dir, "sent", "aa bb. cc dd\nee ff\n")
	sent2 := ptxFile(t, dir, "sent2", "aa bb.  cc dd\nee ff\n")
	sent3 := ptxFile(t, dir, "sent3", "aa bb.\ncc dd\n")
	distinct := ptxFile(t, dir, "distinct", "gg hh. ii jj\nkk ll\n")
	closers := ptxFile(t, dir, "closers", "aa bb.\")  cc dd\nee ff\n")
	tabsep := ptxFile(t, dir, "tabsep", "aa bb.\tcc dd\n")
	longline := ptxFile(t, dir, "longline",
		"alpha bravo charlie delta echo foxtrot golf hotel india juliet\n")
	mixedcase := ptxFile(t, dir, "mixedcase", "Apple banana APPLE Banana cherry\n")
	classes := ptxFile(t, dir, "classes", "a.b c!d foo_bar baz-qux 123 caf\xc3\xa9\n")
	nonl := ptxFile(t, dir, "nonl", "aa bb")
	joinl := ptxFile(t, dir, "joinl", "zz yy")
	empty := ptxFile(t, dir, "empty", "")
	blanks := ptxFile(t, dir, "blanks", "  aa bb\n   cc\n")
	ctrl := ptxFile(t, dir, "ctrl", "aa\r\vbb\f\x01cc\x80dd\tee\n")
	nul := ptxFile(t, dir, "nul", "aa\x00bb cc\n")
	quotes := ptxFile(t, dir, "quotes", "aa\"bb 'cc' \\dd\n")
	tex := ptxFile(t, dir, "tex", "it's (a) {b} $c% &d# e_f \\g ^h ~i\n")
	var sweep strings.Builder
	for c := 32; c < 127; c++ {
		sweep.WriteString("aa")
		sweep.WriteByte(byte(c))
		sweep.WriteString("bb\n")
	}
	bytesweep := ptxFile(t, dir, "bytesweep", sweep.String())
	var even, odd strings.Builder
	for i := 0; i < 40; i++ {
		even.WriteString(string(rune('a'+i%26)) + string(rune('a'+i/26)) + " ")
	}
	for i := 0; i < 24; i++ {
		odd.WriteString(strings.Repeat(string(rune('a'+i%26)), 1+i%7) + " ")
	}
	evenWords := ptxFile(t, dir, "even", strings.TrimRight(even.String(), " ")+"\n")
	oddWords := ptxFile(t, dir, "odd", strings.TrimRight(odd.String(), " ")+"\n")
	numbered := ptxFile(t, dir, "numbered",
		"w01 w02 w03 w04 w05 w06 w07 w08 w09 w10 w11 w12 w13 w14 w15 w16\n")
	var many strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&many, "word%d", 100000+i*7%20000)
		if i%10 == 9 {
			many.WriteByte('\n')
		} else {
			many.WriteByte(' ')
		}
	}
	manyWords := ptxFile(t, dir, "many", many.String())
	longword := ptxFile(t, dir, "longword", "xx abcdefghijklmnopqrstuvwxyz yy\n")
	head1 := ptxFile(t, dir, "head1",
		"j obu zpqfnsbg lrabgsro jxbmte liwe hlna zjodid ichiglx mz dclj oy"+
			" cmychrc ypj eruip h cz odqhvyi ghnkytxi btikugzb rsxwry itzsk\n")
	head2 := ptxFile(t, dir, "head2",
		"zqud nac ehahjep zbkpwl bnj xxka rveqzsg borl wbbsz xfa giaie\n")
	head3 := ptxFile(t, dir, "head3",
		"t d ricy vf ulpp gkkpxe zd ztyygonx mzele jryxfn sdokcr ns"+
			" vojyacjg wj xywkjehl ldkwos oqzjo hmxqhc lalvms\n")
	head4 := ptxFile(t, dir, "head4", "mg b joelap w gg dpjdd fesm hqu jjn wccin v\n")
	head5 := ptxFile(t, dir, "head5", "!#ei 66694 pmmzffz diqbu &{gxa 0 }wz fyjr _rgod"+
		" zmq olumn %e wdvznr ycelbh zb (v 09397 5685 .#z\n")
	huge := ptxFile(t, dir, "huge", "q "+strings.Repeat("z", 200)+"\n")

	refs := ptxFile(t, dir, "refs", "REF1 alpha beta gamma\nLONGREF2 delta epsilon\n")
	refTab := ptxFile(t, dir, "reftab", "REF\talpha beta\n")
	refSpaces := ptxFile(t, dir, "refspaces", "REF   alpha beta\n")
	refOnly := ptxFile(t, dir, "refonly", "solo\n")
	refMid := ptxFile(t, dir, "refmid", "R1 a b\nsolo\nR3 c d\n")
	refLead := ptxFile(t, dir, "reflead", "  leading ref\n")
	refBlank := ptxFile(t, dir, "refblank", "R1 a b\n\nR3 c d\n")
	blankOnly := ptxFile(t, dir, "blankonly", "\n")
	wsLine := ptxFile(t, dir, "wsline", "R1 a b\n   \nR3 c d\n")
	twelve := ptxFile(t, dir, "twelve", strings.Repeat("alpha beta\n", 12))

	brk := ptxFile(t, dir, "brk", "xyz\n")
	brkEmpty := ptxFile(t, dir, "brkempty", "")
	brkTab := ptxFile(t, dir, "brktab", "\t")
	brkInput := ptxFile(t, dir, "brkinput", "axb ayb\n")
	brkZ := ptxFile(t, dir, "brkz", "z")
	brkAcross := ptxFile(t, dir, "brkacross", "aa bb.  cc dd.  ee ff\nzz gg hh\n")
	ign := ptxFile(t, dir, "ign", "banana\ncherry\n")
	ign2 := ptxFile(t, dir, "ign2", "banana cherry\n")
	ign3 := ptxFile(t, dir, "ign3", "\nbanana\n\n")
	ign4 := ptxFile(t, dir, "ign4", "banana")
	only := ptxFile(t, dir, "only", "banana\n")

	missing := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")
	spaced := ptxFile(t, dir, "f name", "aa bb\n")
	raw := rawByteFile(t, dir, "aa bb\n")

	return []invocation{
		{name: "default", args: []string{in1}},
		{name: "roff", args: []string{"-O", in1}},
		{name: "tex", args: []string{"-T", in1}},
		{name: "traditional", args: []string{"-G", in1}},
		{name: "traditional tex", args: []string{"-G", "-T", in1}},
		{name: "traditional roff", args: []string{"-G", "-O", in1}},
		{name: "format roff long", args: []string{"--format=roff", in1}},
		{name: "format tex long", args: []string{"--format=tex", in1}},
		{name: "format abbreviated r", args: []string{"--format=r", in1}},
		{name: "format abbreviated t", args: []string{"--format=t", in1}},
		{name: "format separate argument", args: []string{"--format", "tex", in1}},
		{name: "O takes no argument", args: []string{"-O", "roff", in1}},
		{name: "last format wins", args: []string{"-O", "-T", in1}},
		{name: "last format wins the other way", args: []string{"-T", "-O", in1}},
		{name: "long format then short", args: []string{"--format=tex", "-O", in1}},
		{name: "no operand reads stdin", stdin: "aa bb cc\n"},
		{name: "lone dash is stdin", args: []string{"-"}, stdin: "aa bb cc\n"},
		{name: "empty operand is stdin", args: []string{""}, stdin: "alpha beta\n"},
		{name: "dashdash then stdin", args: []string{"--"}, stdin: "aa bb\n"},

		{name: "sentence one space does not split", args: []string{"-O", sent}},
		{name: "sentence two spaces split", args: []string{"-O", sent2}},
		{name: "sentence end of line splits", args: []string{"-O", sent3}},
		{name: "sentence tab splits", args: []string{"-O", tabsep}},
		{name: "sentence closers", args: []string{"-O", closers}},
		{name: "sentence end of line starts the next line's context", stdin: "a b.\nthe c d\n"},
		{name: "sentence blanks stay out of both contexts", stdin: "x y.  \n  the c d.\t e f\n"},
		{name: "traditional lines", args: []string{"-G", "-O", sent}},
		{name: "sentence regexp", args: []string{"-O", "-S", "\\.", sent2}},
		{name: "sentence regexp newline", args: []string{"-O", "-S", "\\n", in1}},
		{name: "sentence regexp escape tab", args: []string{"-O", "-S", "\\t", tabsep}},
		{name: "empty sentence regexp leaves one context", args: []string{"-O", "-S", "", sent2}},
		{name: "sentence regexp in traditional", args: []string{"-G", "-O", "-S", "zzz", in1}},
		{name: "blank line does not end a context", args: []string{"-O"}, stdin: "aa\n\nbb\n"},
		{name: "empty context is fatal", args: []string{"-O", "-S", "x"}, stdin: "aa bbxxcc dd\n"},
		{name: "leading separator is fatal", args: []string{"-O", "-S", "x"}, stdin: "xaa bb\n"},
		{name: "context with leading blanks", args: []string{blanks}},
		{name: "context with leading blanks roff", args: []string{"-O", blanks}},

		{name: "letters only", args: []string{"-T", classes}},
		{name: "traditional non whitespace", args: []string{"-G", "-T", classes}},
		{name: "word regexp", args: []string{"-O", "-W", "[a-z]+", mixedcase}},
		{name: "word regexp plus is an operator", args: []string{"-O", "-W", "a+", mixedcase}},
		{name: "word regexp alternation", args: []string{"-O", "-W", "aa\\|bb", sent}},
		{name: "word regexp group", args: []string{"-O", "-W", "\\(aa\\)", sent}},
		{name: "word regexp has no intervals", args: []string{"-O", "-W", "a\\{2\\}", sent}},
		{name: "word regexp has no brace intervals", args: []string{"-O", "-W", "a{2}", sent}},
		{name: "word regexp has no classes", args: []string{"-O", "-W", "[[:alpha:]]", sent}},
		{name: "word regexp escape tab", args: []string{"-G", "-O", "-W", "\\t", tabsep}},
		{name: "empty word regexp is not given", args: []string{"-O", "-W", "", mixedcase}},
		{name: "word regexp overrides break file", args: []string{"-O", "-W", "[a-z]+", "-b", brk, brkInput}},
		{name: "break file", args: []string{"-O", "-b", brk, brkInput}},
		{name: "break file traditional", args: []string{"-G", "-O", "-b", brk, brkInput}},
		{name: "empty break file", args: []string{"-O", "-b", brkEmpty, brkInput}},
		{name: "empty break file traditional", args: []string{"-G", "-O", "-b", brkEmpty, brkInput}},
		{name: "break file is not escape processed", args: []string{"-O", "-b", brkTab, tabsep}},
		{name: "a word running past a sentence is cut at it", args: []string{"-b", brkZ, brkAcross}},
		{name: "a word running past a sentence is cut at it in roff", args: []string{"-O", "-b", brkZ, brkAcross}},
		{name: "break file from stdin", args: []string{"-O", "-b", "-", brkInput}, stdin: "xyz\n"},
		{name: "break file empty name is stdin", args: []string{"-O", "-b", "", brkInput}, stdin: "xyz\n"},
		{name: "control bytes", args: []string{ctrl}},
		{name: "control bytes roff", args: []string{"-O", ctrl}},
		{name: "a NUL", args: []string{"-O", nul}},
		{name: "non utf8 input", args: []string{"-T", classes}},

		{name: "ignore file", args: []string{"-G", "-O", "-i", ign, mixedcase}},
		{name: "ignore is case sensitive with fold", args: []string{"-G", "-O", "-f", "-i", ign, mixedcase}},
		{name: "ignore two words on a line", args: []string{"-G", "-O", "-i", ign2, mixedcase}},
		{name: "ignore blank lines", args: []string{"-G", "-O", "-i", ign3, mixedcase}},
		{name: "ignore without a trailing newline", args: []string{"-G", "-O", "-i", ign4, mixedcase}},
		{name: "ignore from stdin", args: []string{"-G", "-O", "-i", "-", mixedcase}, stdin: "banana\ncherry\n"},
		{name: "only file", args: []string{"-G", "-O", "-o", only, mixedcase}},
		{name: "only two words on a line", args: []string{"-G", "-O", "-o", ign2, mixedcase}},
		{name: "ignore and only together", args: []string{"-G", "-O", "-i", ign, "-o", only, mixedcase}},
		{name: "last ignore wins", args: []string{"-G", "-O", "-i", ign, "-i", only, mixedcase}},
		{name: "only does not change the widest keyword", args: []string{"-o", only, longline}},

		{name: "fold", args: []string{"-G", "-O", "-f", mixedcase}},
		{name: "no fold", args: []string{"-G", "-O", mixedcase}},
		{name: "ties keep input order", args: []string{"-G", "-O"}, stdin: "zzz the\naaa the\n"},
		{name: "two files interleave by position", args: []string{"-O", in1, in1}},
		{name: "contexts do not join across files", args: []string{"-O", sent3, distinct}},
		{name: "unterminated file does not join the next", args: []string{"-O", joinl, sent}},

		{name: "width 20", args: []string{"-w", "20", in1}},
		{name: "width 30", args: []string{"-w", "30", in1}},
		{name: "width 31 is width 30", args: []string{"-w", "31", in1}},
		{name: "width 40", args: []string{"-w", "40", in1}},
		{name: "width 50", args: []string{"-w", "50", in1}},
		{name: "width 100", args: []string{"-w", "100", in1}},
		{name: "width 1", args: []string{"-w", "1", in1}},
		{name: "width 5", args: []string{"-w", "5", in1}},
		{name: "width 15", args: []string{"-w", "15", in1}},
		{name: "gap 1", args: []string{"-g", "1", in1}},
		{name: "gap 2", args: []string{"-g", "2", in1}},
		{name: "gap 5", args: []string{"-g", "5", in1}},
		{name: "gap 8", args: []string{"-g", "8", in1}},
		{name: "gap larger than the width", args: []string{"-w", "5", "-g", "10", in1}},
		{name: "gap 100", args: []string{"-g", "100", in1}},
		{name: "width and gap", args: []string{"-w", "40", "-g", "5", in1}},
		{name: "width hex", args: []string{"-w", "0x14", in1}},
		{name: "width octal", args: []string{"-w", "024", in1}},
		{name: "width binary", args: []string{"-w", "0b10100", in1}},
		{name: "width binary uppercase", args: []string{"-w", "0B10100", in1}},
		{name: "width binary signed", args: []string{"-w", "+0b10100", in1}},
		{name: "gap binary", args: []string{"-g", "0b10", in1}},
		{name: "gap binary uppercase", args: []string{"-g", "0B10", in1}},
		{name: "width binary missing digits", args: []string{"-w", "0b", in1}},
		{name: "width binary invalid digit", args: []string{"-w", "0b102", in1}},
		{name: "width binary zero", args: []string{"-w", "0b0", in1}},
		{name: "width binary negative", args: []string{"-w", "-0b1", in1}},
		{name: "width binary overflow", args: []string{"-w", "0b1" + strings.Repeat("0", 63), in1}},
		{name: "gap binary suffix", args: []string{"-g", "0b10x", in1}},
		{name: "gap hex", args: []string{"-g", "0x2", in1}},
		{name: "width bounds the roff fields", args: []string{"-O", "-w", "20", in1}},
		{name: "width bounds the tex fields", args: []string{"-T", "-w", "20", in1}},
		{name: "last width wins", args: []string{"-w", "30", "-w", "20", in1}},

		{name: "even words", args: []string{evenWords}},
		{name: "even words narrow", args: []string{"-w", "40", evenWords}},
		{name: "even words roff", args: []string{"-O", evenWords}},
		{name: "even words traditional", args: []string{"-G", "-O", evenWords}},
		{name: "odd words", args: []string{oddWords}},
		{name: "odd words roff", args: []string{"-O", oddWords}},
		{name: "many words", args: []string{manyWords}},
		{name: "many words traditional", args: []string{"-G", manyWords}},
		{name: "many words folded", args: []string{"-f", manyWords}},
		{name: "numbered words", args: []string{numbered}},
		{name: "numbered words narrow", args: []string{"-w", "40", numbered}},
		{name: "numbered words gap 1", args: []string{"-g", "1", numbered}},
		{name: "a keyword wider than its field", args: []string{"-O", "-w", "20", longword}},
		{name: "a keyword wider than its field in tex", args: []string{"-T", "-w", "20", longword}},
		{name: "a keyword wider than its field in columns", args: []string{"-w", "20", longword}},
		{name: "one very long word", args: []string{huge}},
		{name: "one very long word roff", args: []string{"-O", huge}},
		{name: "one very long word narrow", args: []string{"-w", "30", huge}},
		{name: "head width 72 gap 2", args: []string{"-w", "72", "-g", "2", head1}},
		{name: "head width 50 gap 1", args: []string{"-w", "50", "-g", "1", head2}},
		{name: "head width 50 gap 2", args: []string{"-w", "50", "-g", "2", head3}},
		{name: "head unflagged at the context start", args: []string{"-w", "72", "-g", "2", head4}},
		{name: "head with punctuation against the keyword", args: []string{"-w", "40", "-g", "2", head5}},
		{name: "head roff", args: []string{"-O", "-w", "72", "-g", "2", head1}},
		{name: "head tex", args: []string{"-T", "-w", "50", "-g", "1", head2}},
		{name: "head traditional", args: []string{"-G", "-w", "50", "-g", "2", head3}},
		{name: "head with a flag string", args: []string{"-F", "XX", "-w", "50", "-g", "2", head3}},
		{name: "an empty flagged before keeps the tail column", args: []string{"-w", "20", "-g", "2", head3}},

		{name: "flag string", args: []string{"-F", "XX", "-w", "30", in1}},
		{name: "empty flag string", args: []string{"-F", "", "-w", "30", in1}},
		{name: "long flag string", args: []string{"-F", "****", "-w", "40", in1}},
		{name: "flag string in roff", args: []string{"-O", "-F", "XX", "-w", "20", in1}},
		{name: "flag string is emitted raw in roff", args: []string{"-O", "-F", "\"", "-w", "30", longline}, rawByteName: true},
		{name: "flag escape bel", args: []string{"-F", "\\007", "-w", "14", in1}},
		{name: "flag escape hex", args: []string{"-F", "\\x41", "-w", "14", in1}},
		{name: "flag escape short octal", args: []string{"-F", "\\07", "-w", "14", in1}},
		{name: "flag escape four octal digits", args: []string{"-F", "\\0101", "-w", "14", in1}},
		{name: "flag octal needs a leading zero", args: []string{"-F", "\\101", "-w", "14", in1}},
		{name: "flag escape hex truncates", args: []string{"-F", "\\x414", "-w", "14", in1}},
		{name: "flag bare backslash x", args: []string{"-F", "\\x", "-w", "14", in1}},
		{name: "flag unknown escape", args: []string{"-F", "\\q", "-w", "14", in1}},
		{name: "flag double backslash", args: []string{"-F", "\\\\", "-w", "14", in1}},
		{name: "flag escape c truncates", args: []string{"-F", "\\cX", "-w", "14", in1}},
		{name: "flag escape e is literal", args: []string{"-F", "\\e", "-w", "14", in1}},
		{name: "flag NUL is empty", args: []string{"-F", "\\0", "-w", "14", in1}},
		{name: "flag tab", args: []string{"-F", "\\t", "-w", "14", in1}},

		{name: "macro name", args: []string{"-O", "-M", "ZZ", in1}},
		{name: "macro name in tex", args: []string{"-T", "-M", "ZZ", in1}},
		{name: "empty macro name", args: []string{"-O", "-M", "", in1}},
		{name: "macro name is not escape processed", args: []string{"-O", "-M", "a\\tb", sent}},
		{name: "macro name is emitted raw", args: []string{"-O", "-M", "a\"b", sent}, rawByteName: true},
		{name: "last macro wins", args: []string{"-O", "-M", "AA", "-M", "BB", in1}},
		{name: "macro name does nothing in columns", args: []string{"-M", "ZZ", in1}},

		{name: "roff doubles the quote", args: []string{"-G", "-O", quotes}},
		{name: "tex escapes its eight", args: []string{"-G", "-T", tex}},
		{name: "roff over every printable byte", args: []string{"-G", "-O", bytesweep}},
		{name: "tex over every printable byte", args: []string{"-G", "-T", bytesweep}},
		{name: "columns over every printable byte", args: []string{"-G", bytesweep}},

		{name: "input references", args: []string{"-O", refs}},
		{name: "input references columns", args: []string{refs}},
		{name: "input references tex", args: []string{"-T", refs}},
		{name: "input references right", args: []string{"-R", "-r", refs}},
		{name: "input references right roff is a no-op", args: []string{"-O", "-R", "-r", refs}},
		{name: "input references right tex is a no-op", args: []string{"-T", "-R", "-r", refs}},
		{name: "input reference tab separated", args: []string{"-O", "-r", refTab}},
		{name: "input reference many spaces", args: []string{"-O", "-r", refSpaces}},
		{name: "a reference only line makes nothing", args: []string{"-O", "-r", refOnly}},
		{name: "a reference only line in the middle", args: []string{"-O", "-r", refMid}},
		{name: "a line starting with a blank has an empty reference", args: []string{"-O", "-r", refLead}},
		{name: "a blank line is fatal under r", args: []string{"-O", "-r", refBlank}},
		{name: "a lone blank line is fatal under r", args: []string{"-O", "-r", blankOnly}},
		{name: "a whitespace line is not", args: []string{"-O", "-r", wsLine}},
		{name: "r makes contexts lines", args: []string{"-O", "-r", sent2}},
		{name: "r with a sentence regexp", args: []string{"-O", "-r", "-S", "\\.", refs}},
		{name: "auto references", args: []string{"-A", in1}},
		{name: "auto references roff", args: []string{"-A", "-O", in1}},
		{name: "auto references tex", args: []string{"-A", "-T", in1}},
		{name: "auto references right", args: []string{"-A", "-R", in1}},
		{name: "auto references keep sentence contexts", args: []string{"-A", "-O", sent2}},
		{name: "auto reference for stdin", args: []string{"-A", "-O"}, stdin: "aa bb\n"},
		{name: "auto reference for a dash operand", args: []string{"-A", "-O", "-"}, stdin: "aa bb\n"},
		{name: "auto reference uses the operand spelling", args: []string{"-A", "-O", spaced}},
		{name: "auto references widen at ten lines", args: []string{"-A", twelve}},
		{name: "auto and input references together", args: []string{"-A", "-r", refs}},
		{name: "auto and input references roff", args: []string{"-A", "-r", "-O", refs}},
		{name: "references narrow", args: []string{"-A", "-w", "40", in1}},
		{name: "input references narrow", args: []string{"-r", "-w", "40", refs}},
		{name: "right references narrow", args: []string{"-A", "-R", "-w", "40", in1}},
		{name: "right side refs with no reference at all", args: []string{"-R", in1}},
		{name: "references over two files", args: []string{"-A", "-O", in1, sent}},

		{name: "traditional writes its second operand", args: []string{"-G", "in", "out"},
			seedTree: ptxSeed},
		{name: "traditional output named dash", args: []string{"-G", "in", "-"},
			seedTree: ptxSeed},
		{name: "traditional output over an existing file", args: []string{"-G", "in", "keep"},
			seedTree: ptxSeed},
		{name: "traditional input is its own output", args: []string{"-G", "keep", "keep"},
			seedTree: ptxSeed},
		{name: "traditional truncates before a missing input", args: []string{"-G", "nosuch", "keep"},
			seedTree: ptxSeed},
		{name: "traditional truncates before a bad regexp", args: []string{"-G", "-W", "[", "in", "keep"},
			seedTree: ptxSeed},
		{name: "traditional truncates before an extra operand", args: []string{"-G", "in", "keep", "extra"},
			seedTree: ptxSeed},
		{name: "a numeric error comes before the truncation", args: []string{"-G", "-w", "0", "in", "keep"},
			seedTree: ptxSeed},
		{name: "traditional stdin to a file", args: []string{"-G", "-", "out"},
			stdin: "aa bb\n", seedTree: ptxSeed},

		{name: "typeset mode is inert", args: []string{"-G", "-t", mixedcase}},

		{name: "glued short value", args: []string{"-w20", "-G", "-O", in1}},
		{name: "long with equals", args: []string{"--width=20", "-G", "-O", in1}},
		{name: "long with a separate value", args: []string{"--width", "20", "-G", "-O", in1}},
		{name: "clustered with the valued option last", args: []string{"-Gw20", "-O", in1}},
		{name: "clustered with a separate value", args: []string{"-OGw", "20", in1}},
		{name: "options permute past operands", args: []string{in1, "-G"}},
		{name: "long option after an operand", args: []string{in1, "--traditional"}},
		{name: "posix stops permutation", args: []string{in1, "-G"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix with the option first", args: []string{"-G", in1}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "dashdash makes an option a name", args: []string{"--", "-G", in1}},
		{name: "dashdash before a real operand", args: []string{"-O", "--", sent}},

		{name: "invalid short option", args: []string{"-x", in1}},
		{name: "invalid option in a cluster", args: []string{"-Gx", in1}},
		{name: "unrecognized long option", args: []string{"--foo", in1}},
		{name: "unrecognized long option with a value", args: []string{"--foo=bar", in1}},
		{name: "empty long name is ambiguous", args: []string{"--=x", in1}},
		{name: "i is ambiguous", args: []string{"--i", in1}},
		{name: "ignore is ambiguous", args: []string{"--ignore", in1}},
		{name: "ignore-c is unique", args: []string{"--ignore-c", "-G", "-O", mixedcase}},
		{name: "ignore-f is unique", args: []string{"--ignore-f", ign, "-G", "-O", mixedcase}},
		{name: "f is ambiguous", args: []string{"--f", in1}},
		{name: "r is ambiguous", args: []string{"--r", in1}},
		{name: "t is ambiguous", args: []string{"--t", in1}},
		{name: "a is unique", args: []string{"--a", "-O", in1}},
		{name: "b is unique", args: []string{"--b", brk, "-O", brkInput}},
		{name: "fl is unique", args: []string{"--fl", "XX", "-w", "30", in1}},
		{name: "g is unique", args: []string{"--g", "5", in1}},
		{name: "m is unique", args: []string{"--m", "ZZ", "-O", in1}},
		{name: "o is unique", args: []string{"--o", only, "-G", "-O", mixedcase}},
		{name: "re is unique", args: []string{"--re", "-O", refs}},
		{name: "ri is unique", args: []string{"--ri", "-r", "-O", refs}},
		{name: "s is unique", args: []string{"--s", "\\.", "-O", sent2}},
		{name: "tr is unique", args: []string{"--tr", "-O", in1}},
		{name: "ty is unique", args: []string{"--ty", "-G", "-O", in1}},
		{name: "wi is unique", args: []string{"--wi", "20", in1}},
		{name: "wo is unique", args: []string{"--wo", "[a-z]+", "-O", mixedcase}},
		{name: "width requires an argument", args: []string{"-w"}},
		{name: "long width requires an argument", args: []string{"--width"}},
		{name: "flag rejects a glued value", args: []string{"--traditional=x", in1}},
		{name: "help rejects a glued value", args: []string{"--help=x"}},
		{name: "version rejects a glued value", args: []string{"--version=x"}},
		{name: "invalid format argument", args: []string{"--format=x", in1}},
		{name: "format takes a file as its argument", args: []string{"--format", in1}},

		{name: "zero width", args: []string{"-w", "0", in1}},
		{name: "negative width", args: []string{"-w", "-1", in1}},
		{name: "malformed width", args: []string{"-w", "x", in1}},
		{name: "empty width", args: []string{"-w", "", in1}},
		{name: "width past intmax", args: []string{"-w", "9223372036854775808", in1}},
		{name: "width past uintmax", args: []string{"-w", "18446744073709551614", in1}},
		{name: "zero gap", args: []string{"-g", "0", in1}},
		{name: "malformed gap", args: []string{"-g", "abc", in1}},
		{name: "width is checked before gap", args: []string{"-w", "0", "-g", "0", in1}},
		{name: "gap is checked before width", args: []string{"-g", "0", "-w", "0", in1}},
		{name: "a numeric error beats a bad regexp", args: []string{"-w", "0", "-W", "[", in1}},
		{name: "a numeric error beats a missing file", args: []string{"-w", "0", missing}},

		{name: "bad word regexp", args: []string{"-W", "[", in1}},
		{name: "bad sentence regexp", args: []string{"-S", "[", in1}},
		{name: "unmatched open group", args: []string{"-W", "\\(", in1}},
		{name: "unmatched close group", args: []string{"-W", "\\)", in1}},
		{name: "a bad regexp beats a missing file", args: []string{"-W", "[", missing}},
		{name: "a bad regexp beats a missing ignore file", args: []string{"-W", "[", "-i", missing, in1}},
		{name: "an unterminated interval is not an error", args: []string{"-O", "-W", "a\\{1", sent}},
		{name: "a bad class is not an error", args: []string{"-O", "-W", "[[:foo:]]", sent}},
		{name: "a bare star is not an error", args: []string{"-O", "-W", "*", sent}},

		{name: "missing file", args: []string{missing}},
		{name: "missing file after a good one", args: []string{in1, missing}},
		{name: "directory operand", args: []string{d}},
		{name: "missing ignore file", args: []string{"-i", missing, in1}},
		{name: "missing only file", args: []string{"-o", missing, in1}},
		{name: "missing break file", args: []string{"-b", missing, in1}},
		{name: "list files are opened before the input", args: []string{"-i", missing, missing}},
		{name: "name with a space", args: []string{"-O", spaced}},
		{name: "name that is not valid UTF-8", args: []string{"-O", raw}, rawByteName: true},
		{name: "empty input", args: []string{empty}},
		{name: "empty stdin", stdin: ""},
		{name: "no trailing newline", args: []string{"-O", nonl}},
		{name: "three operands without G", args: []string{in1, sent, missing}},

		{name: "stdout closed", args: []string{in1}, stdout: stdoutClosed},
		{name: "stdout full", args: []string{in1}, stdout: stdoutFull},
		{name: "stdout closed with nothing to write", args: []string{empty}, stdout: stdoutClosed},
		{name: "stdout closed with a usage error", args: []string{"-w", "0", in1}, stdout: stdoutClosed},
		{name: "stdout closed with a missing file", args: []string{missing}, stdout: stdoutClosed},
	}
}

func ptxSeed(t *testing.T, dir string) {
	t.Helper()
	ptxFile(t, dir, "in", "the quick brown fox\njumps over the lazy dog\n")
	ptxFile(t, dir, "keep", "PRESERVE\n")
}

func TestPtxParity(t *testing.T) {
	requireParity(t, "ptx", ptxCases(t))
}

func TestPtxHelpVersion(t *testing.T) {
	requireHelp(t, "ptx", []string{"--help"}, 0)
	requireHelp(t, "ptx", []string{"--h"}, 0)
	requireHelp(t, "ptx", []string{"--hel"}, 0)
	requireVersion(t, "ptx", []string{"--version"}, 0)
	requireVersion(t, "ptx", []string{"--v"}, 0)
	requireVersion(t, "ptx", []string{"--vers"}, 0)
}
