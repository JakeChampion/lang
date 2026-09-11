package coreutils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dircolorsFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// dircolorsEnv is the environment every case has to name: baseEnv sets
// no SHELL, TERM or COLORTERM, and all three change the output.
func dircolorsEnv(shell, term, colorterm string) []string {
	return []string{"SHELL=" + shell, "TERM=" + term, "COLORTERM=" + colorterm}
}

func init() {
	registerCorpus("dircolors", dircolorsCases)
}

// dircolorsCases is dircolors(1)'s corpus.
//
// Three things carry most of the risk. The TERM / COLORTERM filter is a
// four-state machine and not a flag — a non-matching TERM line closes the
// section only when an ordinary line has been read since the one that
// opened it, and the same states decide whether an unknown keyword is
// fatal or ignored — so the section cases come in pairs that differ by
// one intervening line. The pattern is fnmatch(3) with flags 0, whose
// malformed shapes part ways (an unterminated `[` restarts as a literal,
// a bad character class fails the whole match), so the `g*` files put one
// pattern each in front of several TERM values. And the shell escaper is
// a toggle rather than an escape, which only a `^` or a `\` next to a `:`
// shows.
//
// The fourth is the write-failure wording, which is not a property of the
// option but of how many bytes glibc's stdio still had pending at close:
// a body past one buffer gets `write error: <strerror>` from the shell
// emitters, whose suffix is still pending, and a bare `write error` from
// `--print-ls-colors`, which writes one piece and leaves nothing. Hence a
// file either side of that boundary against both emitters and both
// failing descriptors.
//
// The built-in database is GNU coreutils 9.4's, so the cases that reach
// it — `-p`, and every invocation with no FILE — are the version-sensitive
// exception docs/COREUTILS.md allows: a newer reference prints a later
// copyright year and fails them, and the fix is to transcribe its
// `dircolors -p` into coreutils/dircolors.fern.
func dircolorsCases(t *testing.T) []invocation {
	t.Helper()
	dir := t.TempDir()
	linux := dircolorsEnv("/bin/bash", "linux", "")
	vt52 := dircolorsEnv("/bin/bash", "vt52", "")
	truecolor := dircolorsEnv("/bin/bash", "vt52", "truecolor")
	cshEnv := dircolorsEnv("/bin/csh", "linux", "")
	noShell := []string{"TERM=linux", "COLORTERM="}

	f1 := dircolorsFile(t, dir, "f1", "DIR 7\n")
	f2 := dircolorsFile(t, dir, "f2", "DIR 8\n")
	empty := dircolorsFile(t, dir, "empty", "")
	missing := dircolorsFile(t, dir, "missing", "DIR\n")
	nosuch := filepath.Join(dir, "nosuch")
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(dir, "d")

	// The section machine. Each pair differs by one ordinary line
	// between the two TERM lines, which is the whole of the difference
	// between TERMSURE and TERMYES.
	sureThenMiss := dircolorsFile(t, dir, "sure-then-miss", "TERM linux\nTERM vt52\nDIR 3\n")
	yesThenMiss := dircolorsFile(t, dir, "yes-then-miss", "TERM linux\nDIR 2\nTERM vt52\nDIR 3\n")
	sureThenBogus := dircolorsFile(t, dir, "sure-then-bogus", "TERM linux\nTERM vt52\nbogus 1\n")
	yesThenBogus := dircolorsFile(t, dir, "yes-then-bogus", "TERM linux\nDIR 2\nTERM vt52\nbogus 1\n")
	reenable := dircolorsFile(t, dir, "reenable", "TERM vt52\nDIR 2\nTERM linux\nDIR 3\n")
	globalBogus := dircolorsFile(t, dir, "global-bogus", "bogus 1\nDIR 2\n")
	bogusAfterGlobal := dircolorsFile(t, dir, "bogus-after-global", "DIR 2\nbogus 1\n")
	bogusInSection := dircolorsFile(t, dir, "bogus-in-section", "TERM linux\nbogus 1\n")
	bogusAfterIgnored := dircolorsFile(t, dir, "bogus-after-ignored", "TERM linux\nOPTIONS x\nbogus 1\n")
	bogusOffSection := dircolorsFile(t, dir, "bogus-off-section", "TERM vt52\nDIR 2\nbogus 1\n")
	globalThenOff := dircolorsFile(t, dir, "global-then-off", "bogus 1\nTERM vt52\nbogus 2\n")
	offThenOn := dircolorsFile(t, dir, "off-then-on", "TERM vt52\nTERM linux\nbogus 1\n")
	globalSection := dircolorsFile(t, dir, "global-section", "DIR 2\nTERM vt52\nDIR 3\n")
	globalKeeps := dircolorsFile(t, dir, "global-keeps", "DIR 2\nTERM linux\nDIR 3\n")
	colortermLine := dircolorsFile(t, dir, "colorterm-line", "COLORTERM ?*\nbogus 1\nDIR 2\n")
	termNone := dircolorsFile(t, dir, "term-none", "TERM none\nDIR 9\n")
	lowerTerm := dircolorsFile(t, dir, "lower-term", "term linux\nDIR 1\n")
	lowerColorterm := dircolorsFile(t, dir, "lower-colorterm", "colorterm ?*\nDIR 1\n")

	// The line grammar.
	blanks := dircolorsFile(t, dir, "blanks", "#comment\n   \n\n  # c\nDIR 1\n")
	spacing := dircolorsFile(t, dir, "spacing", "  DIR   01;34\n\tLINK\t01;36\n\fSOCK 1\nFIFO \v1\nEXEC 1 \f2\nBLK 1\r\nCHR 1\f\nDOOR 1\v\nSUID 1\t\n")
	restOfLine := dircolorsFile(t, dir, "rest-of-line", "DIR 01;34 extra tokens here\n")
	hashes := dircolorsFile(t, dir, "hashes", "DIR 01;34 # comment\nLINK 01;36#x\n")
	quotesKept := dircolorsFile(t, dir, "quotes-kept", "DIR \"01;34\"\nLINK '01;36'\n")
	noNewline := dircolorsFile(t, dir, "no-newline", "DIR 01;34")
	dupes := dircolorsFile(t, dir, "dupes", "DIR 1\nDIR 2\n")
	caseKw := dircolorsFile(t, dir, "case-kw", ".GZ 5\ndIr 6\n")
	everyKeyword := dircolorsFile(t, dir, "every-keyword", strings.Join([]string{
		"NORMAL 1", "NORM 2", "FILE 3", "RESET 4", "LNK 5", "SYMLINK 6",
		"ORPHAN 7", "MISSING 8", "FIFO 9", "PIPE 10", "SOCK 11", "BLOCK 12",
		"CHAR 13", "DOOR 14", "EXEC 15", "LEFT 16", "LEFTCODE 17", "RIGHT 18",
		"RIGHTCODE 19", "END 20", "ENDCODE 21", "SUID 22", "SETUID 23",
		"SGID 24", "SETGID 25", "STICKY 26", "OTHER_WRITABLE 27", "OWR 28",
		"STICKY_OTHER_WRITABLE 29", "OWT 30", "CAPABILITY 31",
		"MULTIHARDLINK 32", "CLRTOEOL 33", "BLK 34", "CHR 35", "LINK 36",
		"DIR 37", "OPTIONS x", "COLOR yes", "EIGHTBIT 1",
	}, "\n")+"\n")
	extensions := dircolorsFile(t, dir, "extensions", ".gz 1\n*.gz 2\n*# 3\n* 4\n. 5\n*~ 6\n")
	extensionWins := dircolorsFile(t, dir, "extension-wins", "*DIR 1\n.DIR 2\n*TERM linux\n.TERM linux\n*OPTIONS 3\n")
	nuls := dircolorsFile(t, dir, "nuls", "DIR 1\x00extra\nLINK 2\n")
	nulAfterKeyword := dircolorsFile(t, dir, "nul-after-keyword", "DIR\x00 1\n")
	nulFirst := dircolorsFile(t, dir, "nul-first", "\x00DIR 1\n")
	crlf := dircolorsFile(t, dir, "crlf", "TERM linux\r\nDIR 1\r\n")
	keywordOnly := dircolorsFile(t, dir, "keyword-only", "TERM\n")
	twoFaults := dircolorsFile(t, dir, "two-faults", "DIR 1\nbad\nLINK 2\nbad2\n")
	mixedFaults := dircolorsFile(t, dir, "mixed-faults", "TERM *\nzzz 1\nDIR\nyyy 2\n")

	// The escaper's toggle.
	escapes := dircolorsFile(t, dir, "escapes", "DIR a:b\nLINK a=b\nSOCK a^b\nFIFO a\\b\nEXEC a\\:b\nBLK a^:b\nCHR a^^:b\n")
	quotes := dircolorsFile(t, dir, "quotes", "DIR a'b\nLINK a''b\nSOCK a':b\n")
	toggleRuns := dircolorsFile(t, dir, "toggle-runs", "DIR a^^^:b\nLINK a\\^:b\nSOCK a^\\:b\n")
	toggleResets := dircolorsFile(t, dir, "toggle-resets", ".a^ :b\n.a\\ :b\n.a' :b\n")
	bareSpecials := dircolorsFile(t, dir, "bare-specials", "DIR ::\nLINK ==\nSOCK :=:\nFIFO ^\nEXEC \\\nBLK ^^\n")

	// fnmatch: one pattern per file, several TERM values each.
	pattern := func(name, pat string) string {
		return dircolorsFile(t, dir, name, "TERM "+pat+"\nDIR 1\n")
	}
	pStar := pattern("p-star", "*")
	pStarMid := pattern("p-star-mid", "a*b")
	pQuestion := pattern("p-question", "a?b")
	pTwoStars := pattern("p-two-stars", "**")
	pEscStar := pattern("p-esc-star", `a\*b`)
	pBackslashes := pattern("p-backslashes", `\\`)
	pOpenBracket := pattern("p-open-bracket", "[")
	pBracketRB := pattern("p-bracket-rb", "[]a]x")
	pNegate := pattern("p-negate", "[!a-c]x")
	pCaret := pattern("p-caret", "[^a-c]x")
	pDigit := pattern("p-digit", "[[:digit:]]")
	pBadClass := pattern("p-bad-class", "[[:foo:]]")
	pReversed := pattern("p-reversed", "[z-a]")
	pDangling := pattern("p-dangling", `a\`)
	pUnclosedClass := pattern("p-unclosed-class", "[[:digit:]")
	pUnclosed := pattern("p-unclosed", "[abc")
	pUnclosedRange := pattern("p-unclosed-range", "[a-c")
	pDbRange := pattern("p-db-range", "con[0-9]*x[0-9]*")
	pDashInSet := pattern("p-dash-in-set", "[a-c-e]")
	pCollate := pattern("p-collate", "[[.a.]-c]")
	pEquiv := pattern("p-equiv", "[[=a=]b]")
	pPunctRange := pattern("p-punct-range", "[%--]")
	pLeadingDot := pattern("p-leading-dot", ".*")
	pEscInSet := pattern("p-esc-in-set", `[a\]b]`)
	pDashFirst := pattern("p-dash-first", "[-a]")
	pDashLast := pattern("p-dash-last", "[a-]")
	pMultiCollate := pattern("p-multi-collate", "[[.ab.]]")
	pBracketInSet := pattern("p-bracket-in-set", "[[]")
	pColorGlob := pattern("p-color-glob", "*color*")
	pUpperRange := pattern("p-upper-range", "[A-Z]")
	pQuestionStar := pattern("p-question-star", "?*")
	pDanglingInSet := pattern("p-dangling-in-set", `[a\`)
	pNegRB := pattern("p-neg-rb", "[!]a]")
	pClassAndByte := pattern("p-class-and-byte", "[x[:digit:]]")
	pSlashInSet := pattern("p-slash-in-set", "[/]")
	pTwoClasses := pattern("p-two-classes", "[[:upper:][:digit:]]")
	pPunct := pattern("p-punct", "[[:punct:]]")
	pXdigit := pattern("p-xdigit", "[[:xdigit:]]")
	pSpace := pattern("p-space", "[[:space:]]")
	pCntrl := pattern("p-cntrl", "[[:cntrl:]]")
	pGraph := pattern("p-graph", "[[:graph:]]")
	pPrint := pattern("p-print", "[[:print:]]")
	pAlnum := pattern("p-alnum", "[[:alnum:]]")
	pBlank := pattern("p-blank", "[[:blank:]]")
	pClassRange := pattern("p-class-range", "[[:alpha:]-z]")
	pDashRange := pattern("p-dash-range", "[--0]")

	// Past one read block on the way in (90 KB) and past a pipe buffer on
	// the way out (84 KB), which is what the SIGPIPE case needs. Not
	// larger: the self-host leg runs this corpus too, and there an
	// accumulation loop is still quadratic (#9077).
	big := dircolorsFile(t, dir, "big", strings.Repeat("DIR 0123456789\n", 6000))
	// A body that outgrows one stdio buffer, which is what separates the
	// two write-failure wordings: the shell emitters write the body and
	// then a suffix, so the suffix is pending at close and the errno is
	// named, while --print-ls-colors writes one piece and leaves nothing
	// pending, so the same failure is a bare `write error`.
	spill := new(strings.Builder)
	for i := 0; i < 300; i++ {
		fmt.Fprintf(spill, ".ext%04d 01;31\n", i)
	}
	overflow := dircolorsFile(t, dir, "overflow", spill.String())

	against := func(file string, terms ...string) []invocation {
		out := make([]invocation, 0, len(terms))
		for _, term := range terms {
			out = append(out, invocation{
				name: filepath.Base(file) + " vs " + term,
				args: []string{file},
				env:  dircolorsEnv("/bin/bash", term, ""),
			})
		}
		return out
	}

	cases := []invocation{
		// The built-in database and the two shells it is written for.
		{name: "no operand", env: linux},
		{name: "no operand csh", env: cshEnv},
		{name: "no operand no match", env: vt52},
		{name: "no operand colorterm", env: truecolor},
		{name: "no operand con80x25", env: dircolorsEnv("/bin/bash", "con80x25", "")},
		{name: "no operand xterm glob", env: dircolorsEnv("/bin/bash", "xterm-256color", "")},
		{name: "no operand no TERM", env: []string{"SHELL=/bin/bash", "COLORTERM="}},
		{name: "no operand empty TERM", env: []string{"SHELL=/bin/bash", "TERM=", "COLORTERM="}},
		{name: "no operand no COLORTERM", env: []string{"SHELL=/bin/bash", "TERM=vt52"}},
		{name: "LS_COLORS is never read", args: []string{empty}, env: append(linux, "LS_COLORS=xx=1")},

		// -p is not filtered and does not need a shell.
		{name: "print database", args: []string{"-p"}, env: linux},
		{name: "print database no match", args: []string{"-p"}, env: vt52},
		{name: "print database no shell", args: []string{"-p"}, env: noShell},
		{name: "print database long", args: []string{"--print-database"}, env: noShell},
		{name: "print database twice", args: []string{"-p", "-p"}, env: noShell},
		{name: "print database unique prefix", args: []string{"--print-d"}, env: noShell},
		{name: "print database dashdash", args: []string{"-p", "--"}, env: noShell},

		// --print-ls-colors: long only, no shell, no escaping.
		{name: "print ls colors", args: []string{"--print-ls-colors"}, env: linux},
		{name: "print ls colors no shell", args: []string{"--print-ls-colors"}, env: noShell},
		{name: "print ls colors no match", args: []string{"--print-ls-colors"}, env: vt52},
		{name: "print ls colors twice", args: []string{"--print-ls-colors", "--print-ls-colors"}, env: noShell},
		{name: "print ls colors unique prefix", args: []string{"--print-l"}, env: noShell},
		{name: "print ls colors empty file", args: []string{"--print-ls-colors", empty}, env: linux},
		{name: "print ls colors escapes nothing", args: []string{"--print-ls-colors", escapes}, env: linux},
		{name: "print ls colors extensions", args: []string{"--print-ls-colors", extensions}, env: linux},
		{name: "print ls colors every keyword", args: []string{"--print-ls-colors", everyKeyword}, env: linux},
		{name: "print ls colors a directory", args: []string{"--print-ls-colors", d}, env: linux},
		{name: "print ls colors a fault", args: []string{"--print-ls-colors", missing}, env: linux},

		// The shell-syntax options, and the last one winning.
		{name: "bourne", args: []string{"-b"}, env: noShell},
		{name: "csh", args: []string{"-c"}, env: noShell},
		{name: "bourne over SHELL", args: []string{"-b"}, env: cshEnv},
		{name: "csh over SHELL", args: []string{"-c"}, env: linux},
		{name: "sh long", args: []string{"--sh"}, env: noShell},
		{name: "bourne-shell long", args: []string{"--bourne-shell"}, env: noShell},
		{name: "csh long", args: []string{"--csh"}, env: noShell},
		{name: "c-shell long", args: []string{"--c-shell"}, env: noShell},
		{name: "cluster bc", args: []string{"-bc"}, env: noShell},
		{name: "cluster cb", args: []string{"-cb"}, env: noShell},
		{name: "sh then csh", args: []string{"--sh", "--csh", empty}, env: noShell},
		{name: "csh then sh", args: []string{"--csh", "--sh", empty}, env: noShell},

		// Long-option prefixes. `--b`, `--s` and `--c` are unique or
		// same-valued; only the print family is ambiguous.
		{name: "prefix b", args: []string{"--b"}, env: noShell},
		{name: "prefix s", args: []string{"--s"}, env: noShell},
		{name: "prefix c is not ambiguous", args: []string{"--c"}, env: noShell},
		{name: "prefix p", args: []string{"--p"}, env: noShell},
		{name: "prefix pr", args: []string{"--pr"}, env: noShell},
		{name: "prefix print", args: []string{"--print"}, env: noShell},
		{name: "prefix print dash", args: []string{"--print-"}, env: noShell},
		// The empty long option: its list names every candidate but
		// `--sh`, which shares `--bourne-shell`'s id.
		{name: "empty long option", args: []string{"--=x"}, env: noShell},

		// Option faults.
		{name: "invalid option", args: []string{"-x"}, env: noShell},
		{name: "glued digit", args: []string{"-b5"}, env: noShell},
		{name: "glued letter", args: []string{"-cx"}, env: noShell},
		{name: "glued dash is not dashdash", args: []string{"-b-"}, env: noShell},
		{name: "sh takes no argument", args: []string{"--sh=x"}, env: noShell},
		{name: "csh takes no argument", args: []string{"--csh=x"}, env: noShell},
		{name: "print database takes no argument", args: []string{"--print-database=x"}, env: noShell},
		{name: "print ls colors takes no argument", args: []string{"--print-ls-colors=x"}, env: noShell},
		{name: "help takes no argument", args: []string{"--help=x"}, env: noShell},
		{name: "version takes no argument", args: []string{"--version=x"}, env: noShell},
		{name: "unrecognized option", args: []string{"--foo"}, env: noShell},

		// The two mutual exclusions, and that the shell-syntax one runs
		// first: `-b -p --print-ls-colors` says the shell one.
		{name: "bourne and print database", args: []string{"-b", "-p"}, env: noShell},
		{name: "print database and bourne", args: []string{"-p", "-b"}, env: noShell},
		{name: "cluster cp", args: []string{"-cp"}, env: noShell},
		{name: "cluster pb", args: []string{"-pb"}, env: noShell},
		{name: "bourne and print database long", args: []string{"-b", "--print-database"}, env: noShell},
		{name: "bourne and print ls colors", args: []string{"-b", "--print-ls-colors"}, env: noShell},
		{name: "print ls colors and bourne", args: []string{"--print-ls-colors", "-b"}, env: noShell},
		{name: "all three", args: []string{"-b", "-p", "--print-ls-colors"}, env: noShell},
		{name: "shell exclusion beats operand count", args: []string{"--print-ls-colors", "-b", "x", "y"}, env: noShell},
		{name: "the two print options", args: []string{"-p", "--print-ls-colors"}, env: noShell},
		{name: "the two print options reversed", args: []string{"--print-ls-colors", "-p"}, env: noShell},

		// The operand count, and which operand is named.
		{name: "two operands", args: []string{f1, f2}, env: linux},
		{name: "three operands", args: []string{f1, f2, f1}, env: linux},
		{name: "print database with an operand", args: []string{"-p", f1}, env: noShell},
		{name: "print database with two operands", args: []string{"-p", f1, f2}, env: noShell},
		{name: "print database with a dash", args: []string{"-p", "-"}, env: noShell},
		{name: "print ls colors with two operands", args: []string{"--print-ls-colors", f1, f2}, env: noShell},
		// extra operand quotes with quote(), where the file diagnostics
		// use quotef().
		{name: "extra operand with a quote", args: []string{"a", "b'c"}, env: linux},
		{name: "extra operand empty", args: []string{"a", ""}, env: linux},
		{name: "extra operand with a space", args: []string{"a", "a b"}, env: linux},
		{name: "extra operand with a high byte", args: []string{"a", "x\xff"}, env: linux},
		// The count is checked BEFORE the shell is guessed.
		{name: "operand count beats the shell guess", args: []string{f1, f2}, env: noShell},
		{name: "print database operand beats the shell guess", args: []string{"-p", f1}, env: []string{"TERM=linux"}},

		// The shell guess, and that it happens before the file is opened.
		{name: "no shell", env: noShell},
		{name: "no shell with a file", args: []string{f1}, env: noShell},
		{name: "no shell beats a missing file", args: []string{nosuch}, env: noShell},
		{name: "empty shell", env: dircolorsEnv("", "linux", "")},
		{name: "shell csh", env: cshEnv},
		{name: "shell tcsh", env: dircolorsEnv("/bin/tcsh", "linux", "")},
		{name: "shell bare csh", env: dircolorsEnv("csh", "linux", "")},
		{name: "shell bare tcsh", env: dircolorsEnv("tcsh", "linux", "")},
		{name: "shell double slash", env: dircolorsEnv("//csh", "linux", "")},
		{name: "shell double slash mid", env: dircolorsEnv("/bin//csh", "linux", "")},
		{name: "shell xcsh", env: dircolorsEnv("/weird/xcsh", "linux", "")},
		{name: "shell cshx", env: dircolorsEnv("/bin/cshx", "linux", "")},
		{name: "shell upper case", env: dircolorsEnv("/bin/CSH", "linux", "")},
		{name: "shell trailing slash", env: dircolorsEnv("/bin/csh/", "linux", "")},
		{name: "shell bare trailing slash", env: dircolorsEnv("csh/", "linux", "")},
		{name: "shell two trailing slashes", env: dircolorsEnv("/bin/csh//", "linux", "")},
		{name: "shell is a slash", env: dircolorsEnv("/", "linux", "")},
		{name: "print ls colors needs no shell with a file", args: []string{"--print-ls-colors", nosuch}, env: []string{"TERM=linux"}},

		// Operands and `--`.
		{name: "one file", args: []string{f1}, env: linux},
		{name: "one file csh", args: []string{f1}, env: cshEnv},
		{name: "empty file", args: []string{empty}, env: linux},
		{name: "empty file csh", args: []string{empty}, env: cshEnv},
		{name: "dashdash then a file", args: []string{"--", f1}, env: linux},
		{name: "dashdash alone", args: []string{"--"}, env: linux},
		{name: "dashdash then an option-like name", args: []string{"--", "-c"}, env: linux},
		{name: "missing file", args: []string{nosuch}, env: linux},
		{name: "empty name", args: []string{""}, env: linux},
		{name: "name with a quote", args: []string{"--", "a'b"}, env: linux},
		{name: "name with a high byte", args: []string{"--", "ab\xff"}, env: linux},
		{name: "a directory", args: []string{d}, env: linux},

		// Permutation, and POSIXLY_CORRECT stopping it by presence.
		{name: "option after an operand", args: []string{f1, "-c"}, env: linux},
		{name: "posixly correct", args: []string{f1, "-c"}, env: append(linux, "POSIXLY_CORRECT=1")},
		{name: "posixly correct empty", args: []string{f1, "-c"}, env: append(linux, "POSIXLY_CORRECT=")},

		// stdin.
		{name: "dash is stdin", args: []string{"-"}, stdin: "DIR 7\n", env: linux},
		{name: "dash is stdin csh", args: []string{"-"}, stdin: "DIR 7\n", env: cshEnv},
		{name: "empty stdin", args: []string{"-"}, stdin: "", env: linux},
		{name: "stdin with a fault", args: []string{"-"}, stdin: "DIR\n", env: linux},
		{name: "stdin is not read without a dash", stdin: "DIR 7\n", env: linux},
		{name: "stdin is a directory", args: []string{"-"}, stdinPath: d, env: linux},

		// The section machine.
		{name: "sure survives a miss", args: []string{sureThenMiss}, env: linux},
		{name: "yes does not survive a miss", args: []string{yesThenMiss}, env: linux},
		{name: "sure survives a miss then faults", args: []string{sureThenBogus}, env: linux},
		{name: "yes does not survive a miss and is silent", args: []string{yesThenBogus}, env: linux},
		{name: "a section re-enables", args: []string{reenable}, env: linux},
		{name: "global ignores an unknown keyword", args: []string{globalBogus}, env: linux},
		{name: "global ignores it after an entry", args: []string{bogusAfterGlobal}, env: linux},
		{name: "a section does not", args: []string{bogusInSection}, env: linux},
		{name: "an ignored keyword still consumes", args: []string{bogusAfterIgnored}, env: linux},
		{name: "an off section is silent", args: []string{bogusOffSection}, env: linux},
		{name: "global then off", args: []string{globalThenOff}, env: linux},
		{name: "off then on", args: []string{offThenOn}, env: linux},
		{name: "global then a missing section", args: []string{globalSection}, env: linux},
		{name: "global then a matching section", args: []string{globalKeeps}, env: linux},
		{name: "colorterm line unset", args: []string{colortermLine}, env: vt52},
		{name: "colorterm line set", args: []string{colortermLine}, env: truecolor},
		{name: "colorterm line no COLORTERM", args: []string{colortermLine}, env: []string{"SHELL=/bin/bash", "TERM=vt52"}},
		{name: "unset TERM is none", args: []string{termNone}, env: []string{"SHELL=/bin/bash", "COLORTERM="}},
		{name: "empty TERM is none", args: []string{termNone}, env: []string{"SHELL=/bin/bash", "TERM=", "COLORTERM="}},
		{name: "a real TERM is not none", args: []string{termNone}, env: linux},
		{name: "term keyword folds case", args: []string{lowerTerm}, env: linux},
		{name: "colorterm keyword folds case", args: []string{lowerColorterm}, env: truecolor},

		// The line grammar.
		{name: "blank and comment lines", args: []string{blanks}, env: linux},
		{name: "leading and inner whitespace", args: []string{spacing}, env: linux},
		{name: "the argument is the rest of the line", args: []string{restOfLine}, env: linux},
		{name: "a hash ends the argument", args: []string{hashes}, env: linux},
		{name: "quotes are not stripped", args: []string{quotesKept}, env: linux},
		{name: "no trailing newline", args: []string{noNewline}, env: linux},
		{name: "duplicates are appended", args: []string{dupes}, env: linux},
		{name: "keyword case folds, extension case does not", args: []string{caseKw}, env: linux},
		{name: "every keyword", args: []string{everyKeyword}, env: linux},
		{name: "extension keywords", args: []string{extensions}, env: linux},
		{name: "an extension beats the table", args: []string{extensionWins}, env: linux},
		{name: "a NUL cuts the line", args: []string{nuls}, env: linux},
		{name: "a NUL after the keyword", args: []string{nulAfterKeyword}, env: linux},
		{name: "a NUL before the keyword", args: []string{nulFirst}, env: linux},
		{name: "carriage returns are trimmed", args: []string{crlf}, env: linux},
		{name: "keyword with no argument", args: []string{missing}, env: linux},
		{name: "TERM with no argument", args: []string{keywordOnly}, env: linux},
		{name: "two faults are both reported", args: []string{twoFaults}, env: linux},
		{name: "both fault kinds", args: []string{mixedFaults}, env: linux},

		// The escaper.
		{name: "escaper", args: []string{escapes}, env: linux},
		{name: "escaper csh", args: []string{escapes}, env: cshEnv},
		{name: "quotes", args: []string{quotes}, env: linux},
		{name: "toggle runs", args: []string{toggleRuns}, env: linux},
		{name: "the toggle resets per field", args: []string{toggleResets}, env: linux},
		{name: "bare specials", args: []string{bareSpecials}, env: linux},

		// Write failures.
		{name: "stdout closed", stdout: stdoutClosed, env: linux},
		{name: "stdout closed print database", args: []string{"-p"}, stdout: stdoutClosed, env: linux},
		{name: "stdout closed on help", args: []string{"--help"}, stdout: stdoutClosed, env: linux},
		{name: "stdout closed on version", args: []string{"--version"}, stdout: stdoutClosed, env: linux},
		{name: "stdout closed with nothing to write", args: []string{"--print-ls-colors", empty}, stdout: stdoutClosed, env: linux},
		{name: "stdout closed on a parse fault", args: []string{missing}, stdout: stdoutClosed, env: linux},
		{name: "stdout closed print ls colors", args: []string{"--print-ls-colors"}, stdout: stdoutClosed, env: linux},
		{name: "stdout closed csh", args: []string{"-c"}, stdout: stdoutClosed, env: linux},
		{name: "stdout closed on a usage error", args: []string{"-b", "-p"}, stdout: stdoutClosed, env: linux},
		{name: "stdout full", stdout: stdoutFull, env: linux},
		{name: "stdout full print database", args: []string{"-p"}, stdout: stdoutFull, env: linux},
		{name: "stdout full print ls colors", args: []string{"--print-ls-colors"}, stdout: stdoutFull, env: linux},
		{name: "stdout full csh", args: []string{"-c"}, stdout: stdoutFull, env: linux},
		{name: "stdout full with nothing to write", args: []string{"--print-ls-colors", empty}, stdout: stdoutFull, env: linux},
		{name: "stdout full on a parse fault", args: []string{missing}, stdout: stdoutFull, env: linux},

		// A file past one read block, and the same file with the read end
		// closed early: the output outgrows the pipe buffer, so both
		// sides die of SIGPIPE rather than reporting a write error.
		{name: "large file", args: []string{big}, env: linux},
		{name: "large file print ls colors", args: []string{"--print-ls-colors", big}, env: linux},
		{name: "large file onto a full disk", args: []string{"--print-ls-colors", big}, stdout: stdoutFull, env: linux},
		{name: "body past one buffer", args: []string{overflow}, env: linux},
		{name: "body past one buffer onto a full disk", args: []string{overflow}, stdout: stdoutFull, env: linux},
		{name: "body past one buffer csh onto a full disk", args: []string{"-c", overflow}, stdout: stdoutFull, env: linux},
		{name: "body past one buffer print ls colors onto a full disk", args: []string{"--print-ls-colors", overflow}, stdout: stdoutFull, env: linux},
		{name: "body past one buffer onto a closed stdout", args: []string{overflow}, stdout: stdoutClosed, env: linux},
		{name: "body past one buffer print ls colors onto a closed stdout", args: []string{"--print-ls-colors", overflow}, stdout: stdoutClosed, env: linux},
		{name: "large file into a closed pipe", args: []string{big}, env: linux, limit: 64},
	}

	// fnmatch, one pattern against the TERM values that separate it from
	// a glob matcher.
	cases = append(cases, against(pStar, "a/b", "", "x", ".hidden")...)
	cases = append(cases, against(pStarMid, "a/x/b", "ab", "axb")...)
	cases = append(cases, against(pQuestion, "a/b", "ab", "abb")...)
	cases = append(cases, against(pTwoStars, "a/b", "x")...)
	cases = append(cases, against(pEscStar, "a*b", "axb")...)
	cases = append(cases, against(pBackslashes, `\`, `\\`)...)
	cases = append(cases, against(pOpenBracket, "[", "a[")...)
	cases = append(cases, against(pBracketRB, "]x", "ax", "[x")...)
	cases = append(cases, against(pNegate, "dx", "ax", "bx")...)
	cases = append(cases, against(pCaret, "dx", "ax")...)
	cases = append(cases, against(pDigit, "5", "a", "55")...)
	cases = append(cases, against(pBadClass, "x", "[:foo:]")...)
	cases = append(cases, against(pReversed, "b", "z")...)
	cases = append(cases, against(pDangling, `a\`, "a")...)
	cases = append(cases, against(pUnclosedClass, "[5", "5", "[d", "[:", "[t")...)
	cases = append(cases, against(pUnclosed, "[abc", "a", "[b")...)
	cases = append(cases, against(pUnclosedRange, "[a-c", "[b", "b")...)
	cases = append(cases, against(pDbRange, "con80x25", "con8x2", "conx")...)
	cases = append(cases, against(pDashInSet, "d", "-", "e", "a")...)
	cases = append(cases, against(pCollate, "b", "a", "c", "d")...)
	cases = append(cases, against(pEquiv, "b", "a", "c")...)
	cases = append(cases, against(pPunctRange, "&", "%", "-", ".")...)
	cases = append(cases, against(pLeadingDot, ".hidden", "x")...)
	cases = append(cases, against(pEscInSet, "]", `\`, "a", "b")...)
	cases = append(cases, against(pDashFirst, "-", "a", "b")...)
	cases = append(cases, against(pDashLast, "-", "a")...)
	cases = append(cases, against(pMultiCollate, "ab", "a")...)
	cases = append(cases, against(pBracketInSet, "[", "]")...)
	cases = append(cases, against(pColorGlob, "xterm-256color", "color", "xterm")...)
	cases = append(cases, against(pUpperRange, "a", "A")...)
	cases = append(cases, against(pQuestionStar, "x", "xy")...)
	cases = append(cases, against(pDanglingInSet, `[a\`, "[a", "a")...)
	cases = append(cases, against(pNegRB, "b", "]", "a")...)
	cases = append(cases, against(pClassAndByte, "5", "x", "y")...)
	cases = append(cases, against(pSlashInSet, "/", "a")...)
	cases = append(cases, against(pTwoClasses, "A", "5", "a")...)
	cases = append(cases, against(pPunct, "!", "a")...)
	cases = append(cases, against(pXdigit, "f", "g", "5")...)
	cases = append(cases, against(pSpace, "x", " ")...)
	cases = append(cases, against(pCntrl, "x")...)
	cases = append(cases, against(pGraph, "x", " ")...)
	cases = append(cases, against(pPrint, "x", " ")...)
	cases = append(cases, against(pAlnum, "5", "a", "!")...)
	cases = append(cases, against(pBlank, "x", " ")...)
	cases = append(cases, against(pClassRange, "-", "a", "z", "0")...)
	cases = append(cases, against(pDashRange, ".", "-", "0", "a")...)

	return cases
}

func TestDircolorsParity(t *testing.T) {
	requireParity(t, "dircolors", dircolorsCases(t))
}

func TestDircolorsHelpVersion(t *testing.T) {
	requireHelp(t, "dircolors", []string{"--help"}, 0)
	requireHelp(t, "dircolors", []string{"--h"}, 0)
	requireHelp(t, "dircolors", []string{"--he"}, 0)
	// --help and --version short-circuit inside the getopt loop, so they
	// beat every later validation but lose to an earlier getopt fault.
	requireHelp(t, "dircolors", []string{"-b", "-p", "--help"}, 0)
	requireHelp(t, "dircolors", []string{"-p", "--print-ls-colors", "--help"}, 0)
	requireHelp(t, "dircolors", []string{"x", "y", "--help"}, 0)
	requireHelp(t, "dircolors", []string{"--help", "-x"}, 0)
	requireVersion(t, "dircolors", []string{"--version"}, 0)
	requireVersion(t, "dircolors", []string{"--v"}, 0)
	requireVersion(t, "dircolors", []string{"--vers"}, 0)
	requireVersion(t, "dircolors", []string{"--version", "x", "y"}, 0)
}
