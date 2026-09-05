package coreutils

import "testing"

func TestBasenameParity(t *testing.T) {
	requireParity(t, "basename", []invocation{
		// The name rule: last component, trailing slashes dropped, all
		// slashes is `/`, empty stays empty.
		{name: "plain name", args: []string{"a"}},
		{name: "path", args: []string{"/a/b"}},
		{name: "trailing slash", args: []string{"/a/b/"}},
		{name: "many trailing slashes", args: []string{"/a//b//"}},
		{name: "root", args: []string{"/"}},
		{name: "double slash", args: []string{"//"}},
		{name: "triple slash", args: []string{"///"}},
		{name: "empty name", args: []string{""}},
		{name: "dot", args: []string{"."}},
		{name: "dot dot", args: []string{".."}},
		{name: "relative with trailing slash", args: []string{"./a/"}},
		{name: "lone dash is a name", args: []string{"-"}},
		{name: "name that is not valid UTF-8", args: []string{"\xff\xfe"}},

		// The second operand is a suffix, removed only when it is a
		// proper suffix of the name.
		{name: "suffix", args: []string{"a.c", ".c"}},
		{name: "suffix equal to the name", args: []string{"a.c", "a.c"}},
		{name: "suffix once only", args: []string{"a.c.c", ".c"}},
		{name: "suffix after a path", args: []string{"/a/b.c", ".c"}},
		{name: "suffix after a trailing slash", args: []string{"/a/b.c/", ".c"}},
		{name: "suffix that does not match", args: []string{"a/", "b/"}},
		{name: "suffix with a slash never matches", args: []string{"ab/", "b/"}},
		{name: "suffix matches the component", args: []string{"ab/", "b"}},
		{name: "empty suffix", args: []string{"a//", ""}},
		{name: "empty name with a suffix", args: []string{"", "x"}},
		{name: "empty name and suffix", args: []string{"", ""}},
		{name: "dot as name and suffix", args: []string{".", "."}},
		{name: "root with a dot suffix", args: []string{"///", "."}},
		{name: "root with a slash suffix", args: []string{"//", "/"}},
		{name: "suffix that is not valid UTF-8", args: []string{"a\xff\xfe", "\xfe"}},

		// -s implies -a, so every operand is a name; the last -s wins.
		{name: "-s with a slash", args: []string{"-s", "/b", "/a/b"}},
		{name: "-s equal to the name", args: []string{"-s", "b", "b"}},
		{name: "-s empty", args: []string{"-s", "", "a"}},
		{name: "-s makes every operand a name", args: []string{"-s", ".c", "a.c", ".c"}},
		{name: "-s over two names", args: []string{"-s", ".c", "a.c", "b"}},
		{name: "-s proper suffix", args: []string{"-s", "x", "xx"}},
		{name: "-s longer than the name", args: []string{"-s", "xx", "x"}},
		{name: "-s slash on a slash name", args: []string{"-s", "/", "//"}},
		{name: "-s slash on a trailing slash", args: []string{"-s", "/", "a/"}},
		{name: "-s ending in a slash", args: []string{"-s", "b/", "ab/"}},
		{name: "-s on an empty name", args: []string{"-s", "x", ""}},
		{name: "-s glued", args: []string{"-sx", "ax"}},
		{name: "-s glued with an equals sign", args: []string{"-s=x", "a"}},
		{name: "last -s wins", args: []string{"-s", ".c", "-s", ".d", "a.d"}},
		{name: "last -s wins over the first match", args: []string{"-a", "-s", ".c", "-s", ".d", "a.c"}},
		{name: "-a with an empty -s", args: []string{"-a", "-s", "", "a"}},
		{name: "--suffix glued", args: []string{"--suffix=.c", "a.c"}},
		{name: "--suffix separate", args: []string{"--suffix", ".c", "--multiple", "a.c"}},
		{name: "--suffix abbreviated", args: []string{"--suf", ".c", "a.c"}},
		{name: "--suffix by one letter", args: []string{"--s", ".c", "a.c"}},
		{name: "--suffix by two letters", args: []string{"--su", ".c", "a.c"}},
		{name: "-s without a value", args: []string{"-s"}},
		{name: "-a -s without a value", args: []string{"-a", "-s"}},
		{name: "--suffix without a value", args: []string{"--suffix"}},
		{name: "-s with the option as its value", args: []string{"-s", "-a", "x"}},
		{name: "-s and a name only", args: []string{"-s", ".c"}},

		// -a: every operand is a name.
		{name: "-a over three names", args: []string{"-a", "a", "b", "c"}},
		{name: "-a with no names", args: []string{"-a"}},
		{name: "-a then dashdash only", args: []string{"-a", "--"}},
		{name: "--multiple abbreviated", args: []string{"--m", "a"}},
		{name: "-a -s over two names", args: []string{"-a", "-s", ".c", "a.c", "b.c"}},
		{name: "-sa cluster", args: []string{"-sa", "a"}},

		// -z: NUL terminators.
		{name: "-z", args: []string{"-z", "a"}},
		{name: "-z with a suffix operand", args: []string{"-z", "a", "b"}},
		{name: "-z on an empty name", args: []string{"-z", ""}},
		{name: "-a -z", args: []string{"-a", "-z", "a", "b"}},
		{name: "-az cluster", args: []string{"-az", "a", "b"}},
		{name: "-az on empty names", args: []string{"-az", "", ""}},
		{name: "--zero abbreviated", args: []string{"--z", "a"}},

		// The scan is in order: the first operand ends the options.
		{name: "option after the name is a suffix", args: []string{"a", "-a"}},
		{name: "long option after the name is a suffix", args: []string{"a", "--zero"}},
		{name: "option as a third operand", args: []string{"a", "b", "--zero"}},
		{name: "-a as a third operand", args: []string{"a", "b", "-a"}},
		{name: "-s as a third operand", args: []string{"a", "b", "-s", "x"}},
		{name: "-z as a third operand", args: []string{"a", "b", "-z"}},
		{name: "-z among four operands", args: []string{"a", "b", "-z", "c"}},
		{name: "dashdash as a third operand", args: []string{"a", "b", "--", "-z"}},
		{name: "-a after three operands", args: []string{"a", "b", "c", "-a"}},
		{name: "help after the name is a suffix", args: []string{"a", "--help"}},
		{name: "posix changes nothing", args: []string{"a", "-a"}, env: []string{"POSIXLY_CORRECT=1"}},
		{name: "posix option before the name", args: []string{"-a", "a", "b"}, env: []string{"POSIXLY_CORRECT=1"}},

		// Operand counts.
		{name: "no arguments"},
		{name: "three operands", args: []string{"a", "b", "c"}},
		{name: "four operands name the third", args: []string{"a", "b", "c", "d"}},
		{name: "three operands after dashdash", args: []string{"--", "a", "b", "c"}},
		{name: "suffix repeated is an extra operand", args: []string{"a", ".c", ".c"}},
		{name: "dashdash then an option-like name", args: []string{"--", "-a"}},
		{name: "dashdash then help", args: []string{"--", "--help"}},
		{name: "-s then dashdash", args: []string{"-s", "x", "--", "a"}},

		// Getopt faults, worded as glibc words them.
		{name: "invalid short option", args: []string{"-x", "a"}},
		{name: "invalid short option after a valid one", args: []string{"-ax", "a"}},
		{name: "invalid short option before a valid one", args: []string{"-xa", "a"}},
		{name: "unrecognized long option", args: []string{"--foo", "a"}},
		{name: "unrecognized long option with a newline", args: []string{"--foo=a\nb", "x"}},
		{name: "empty long option is ambiguous", args: []string{"--=x", "a"}},
		{name: "--multiple with a value", args: []string{"--multiple=x", "a"}},
		{name: "--zero with a value", args: []string{"--zero=x", "a"}},
		{name: "help with a value", args: []string{"--help=x"}},
		{name: "bad option before help", args: []string{"--foo", "--help"}},

		// The extra operand is quoted in the C locale's style.
		{name: "extra operand with a space", args: []string{"a", "b", "c d"}},
		{name: "extra operand with a newline", args: []string{"a", "b", "c\nd"}},
		{name: "extra operand with a tab", args: []string{"a", "b", "c\td"}},
		{name: "extra operand with the named controls", args: []string{"a", "b", "c\a\b\f\v\rd"}},
		{name: "extra operand with an escape byte", args: []string{"a", "b", "c\x1bd"}},
		{name: "extra operand with a low control", args: []string{"a", "b", "c\x01d"}},
		{name: "extra operand with DEL", args: []string{"a", "b", "c\x7fd"}},
		{name: "extra operand with a high byte", args: []string{"a", "b", "c\xffd"}},
		{name: "extra operand with UTF-8", args: []string{"a", "b", "c\xc3\xa9d"}},
		{name: "extra operand with a quote", args: []string{"a", "b", "c'd"}},
		{name: "extra operand with a double quote", args: []string{"a", "b", "c\"d"}},
		{name: "extra operand with a backslash", args: []string{"a", "b", "\\c"}},
		{name: "extra operand with question marks", args: []string{"a", "b", "c??(d"}},
		{name: "extra operand that is empty", args: []string{"a", "b", ""}},
	})
}

func TestBasenameHelpVersion(t *testing.T) {
	requireHelp(t, "basename", []string{"--help"}, 0)
	requireHelp(t, "basename", []string{"--hel"}, 0)
	requireHelp(t, "basename", []string{"--help", "a"}, 0)
	requireHelp(t, "basename", []string{"--help", "--foo"}, 0)
	requireHelp(t, "basename", []string{"-a", "--help"}, 0)
	requireVersion(t, "basename", []string{"--version"}, 0)
	requireVersion(t, "basename", []string{"--vers"}, 0)
	requireVersion(t, "basename", []string{"--version", "a"}, 0)
}
