package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// std/regex on the self-host IR path through the REAL module import. The
// 2026-06-22 FEATURE-AUDIT recorded array-payload enums (#3720) — std/regex's
// RNode variants RAlt / RSeq / RClass built during the recursive parse — as
// crashing the self-host binary. That gap is now closed for the matching
// surface: importing the real std/regex and calling regex_match compiles the
// recursive RNode construction + the matcher through the self-host IR path and
// matches the interpreter. Each case below exercises a different array-payload
// RNode shape (alternation, character class, grouped repetition), routes "ir"
// through the self-hosted x86-64 loader (asm_load_run), and is oracle-checked
// against the interpreter.
var regexModuleIRCases = []struct {
	name string
	src  string
}{
	// Alternation -> RAlt(RNode[]). "a|b" matches "b".
	{"alt-match", `import "std/regex";
function main(): i32 { if (regex.regex_match("a|b", "b")) { return 42; } return 0; }`},
	// Alternation negative: "a|b" does not match "c".
	{"alt-nomatch", `import "std/regex";
function main(): i32 { if (!regex.regex_match("a|b", "c")) { return 42; } return 0; }`},
	// #6049: alternation whose branches are SEQUENCES — RAlt(RSeq[]) — the
	// shape that separated the six failing regex fixtures from the passing
	// ones. `__rx_alt` builds its branches array out of `RParse.node` field
	// reads, so the un-duped RSeq box was freed by the enclosing `first`'s
	// struct drop and the freelist handed the block back for the RGroup that
	// wraps it: a tree pointing at itself, and `__rx_match` alternated between
	// its RGroup and RAlt arms until the stack ran out. A bare `a|b` (RAlt of
	// RChar) never allocated a branch box, which is why it always passed.
	{"alt-of-seq", `import "std/regex";
function main(): i32 { if (regex.regex_match("(ab|cd)", "xxcdyy")) { return 42; } return 0; }`},
	// #6049 sibling: anchored multi-way alternation, and a SEARCH that re-enters
	// __rx_match once per start position — the second entry is where the swept
	// enum-payload array binding (`RSeq(xs)` / `RAlt(xs)`) had already taken the
	// buffer's count to zero.
	{"anchored-alt", `import "std/regex";
function main(): i32 {
    if (!regex.regex_match("^(cat|dog|bird)$", "dog")) { return 0; }
    if (!regex.regex_match("(foo|barbaz)qux", "aaaa barbazqux")) { return 0; }
    return 42;
}`},
	// Character class + repetition -> RClass / RStar. "[abc]+" matches "cab".
	{"class-plus", `import "std/regex";
function main(): i32 { if (regex.regex_match("[abc]+", "cab")) { return 42; } return 0; }`},
	// Grouped repetition -> RSeq(RNode[]) under a repeat. "(ab)+c" matches "ababc".
	{"group-plus", `import "std/regex";
function main(): i32 { if (regex.regex_match("(ab)+c", "ababc")) { return 42; } return 0; }`},
	// Capturing groups -> RGroup + the compiled RInst capture program
	// (Pike-VM thread simulation with RThread[] state).
	{"captures", `import "std/regex";
function main(): i32 {
    var m: regex.RCaps = regex.regex_captures("(\\d+)-(\\d+)", "order 123-456 shipped");
    if (m.found && m.group(1) == "123" && m.group(2) == "456" && m.group_count() == 2) { return 42; }
    return 0;
}`},
	// Non-capturing (?:...) group + captures_all over multiple matches.
	{"captures-all", `import "std/regex";
function main(): i32 {
    var nc: regex.RCaps = regex.regex_captures("(?:ab)+(c)", "ababc");
    var all: regex.RCaps[] = regex.regex_captures_all("(\\w+)@(\\w+)", "a@b c@d");
    if (nc.group_count() == 1 && nc.group(1) == "c" && all.len() == 2 && all[1].group(2) == "d") { return 42; }
    return 0;
}`},
	// $-template replacement over captures (__rx_expand + __rx_join).
	{"replace-groups", `import "std/regex";
function main(): i32 {
    if (regex.regex_replace_all_groups("(\\w+)@(\\w+)", "a@b c@d", "$2@$1") == "b@a d@c") { return 42; }
    return 0;
}`},
	// Named groups (?<name>…): RGroupData.name payload + the __rx_names walk
	// + ${name} template through the self-host IR path.
	{"named-groups", `import "std/regex";
function main(): i32 {
    var m: regex.RCaps = regex.regex_captures("(?<y>\\d+)-(?<m>\\d+)", "12-34");
    var t: string = regex.regex_replace_groups("(?<w>\\w+)", "hi", "[${w}]");
    if (m.group_named("y") == "12" && m.group_index("m") == 2 && t == "[hi]") { return 42; }
    return 0;
}`},
}

func TestSelfHostRegexModuleIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range regexModuleIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "regex_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "regex_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
