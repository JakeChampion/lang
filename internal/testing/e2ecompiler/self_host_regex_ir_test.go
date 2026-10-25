package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var regexModuleIRCases = []struct {
	name string
	src  string
}{
	{"alt-match", `import "std/regex";
import "std/result";
function main(): i32 { if ({ let __checked_regex_0: regex.Regex = match (regex.compile("a|b")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.is_match("b") }) { return 42; } return 0; }`},
	// Alternation negative: "a|b" does not match "c".
	{"alt-nomatch", `import "std/regex";
import "std/result";
function main(): i32 { if (!{ let __checked_regex_0: regex.Regex = match (regex.compile("a|b")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.is_match("c") }) { return 42; } return 0; }`},
	// #6049: alternation whose branches are SEQUENCES — RAlt(RSeq[]) — the
	// shape that separated the six failing regex fixtures from the passing
	// ones. `__rx_alt` builds its branches array out of `RParse.node` field
	// reads, so the un-duped RSeq box was freed by the enclosing `first`'s
	// struct drop and the freelist handed the block back for the RGroup that
	// wraps it: a tree pointing at itself, and `__rx_match` alternated between
	// its RGroup and RAlt arms until the stack ran out. A bare `a|b` (RAlt of
	// RChar) never allocated a branch box, which is why it always passed.
	{"alt-of-seq", `import "std/regex";
import "std/result";
function main(): i32 { if ({ let __checked_regex_0: regex.Regex = match (regex.compile("(ab|cd)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.is_match("xxcdyy") }) { return 42; } return 0; }`},
	// #6049 sibling: anchored multi-way alternation, and a SEARCH that re-enters
	// __rx_match once per start position — the second entry is where the swept
	// enum-payload array binding (`RSeq(xs)` / `RAlt(xs)`) had already taken the
	// buffer's count to zero.
	{"anchored-alt", `import "std/regex";
import "std/result";
function main(): i32 {
    if (!{ let __checked_regex_1: regex.Regex = match (regex.compile("^(cat|dog|bird)$")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_1.is_match("dog") }) { return 0; }
    if (!{ let __checked_regex_0: regex.Regex = match (regex.compile("(foo|barbaz)qux")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.is_match("aaaa barbazqux") }) { return 0; }
    return 42;
}`},
	{"class-plus", `import "std/regex";
import "std/result";
function main(): i32 { if ({ let __checked_regex_0: regex.Regex = match (regex.compile("[abc]+")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.is_match("cab") }) { return 42; } return 0; }`},
	// Grouped repetition -> RSeq(RNode[]) under a repeat. "(ab)+c" matches "ababc".
	{"group-plus", `import "std/regex";
import "std/result";
function main(): i32 { if ({ let __checked_regex_0: regex.Regex = match (regex.compile("(ab)+c")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.is_match("ababc") }) { return 42; } return 0; }`},
	// Capturing groups -> RGroup + the compiled RInst capture program
	// (Pike-VM thread simulation with RThread[] state).
	{"captures", `import "std/regex";
import "std/result";
function main(): i32 {
    let Some(m) = { let __checked_regex_0: regex.Regex = match (regex.compile("(\\d+)-(\\d+)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.captures("order 123-456 shipped") } else { return 1; };
    if (require_regex_text({ match (m.group(1)) { Ok(__value) => __value, Err(__error) => { eprint(__error.to_string()); return 98; } } }) == "123" && require_regex_text({ match (m.group(2)) { Ok(__value) => __value, Err(__error) => { eprint(__error.to_string()); return 98; } } }) == "456" && m.group_count() == 2) { return 42; }
    return 0;
}
function require_regex_text(result: Option[string]): string {
    match (result) {
        Some(value) => { return value; },
        None => { exit(1); },
    }
    return "";
}
`},
	{"captures-all", `import "std/regex";
import "std/result";
function main(): i32 {
    let Some(nc) = { let __checked_regex_1: regex.Regex = match (regex.compile("(?:ab)+(c)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_1.captures("ababc") } else { return 1; };
    let all: regex.Captures[] = { let __checked_regex_0: regex.Regex = match (regex.compile("(\\w+)@(\\w+)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.captures_all("a@b c@d") };
    if (nc.group_count() == 1 && require_regex_text({ match (nc.group(1)) { Ok(__value) => __value, Err(__error) => { eprint(__error.to_string()); return 98; } } }) == "c" && all.len() == 2 && require_regex_text({ match (all[1].group(2)) { Ok(__value) => __value, Err(__error) => { eprint(__error.to_string()); return 98; } } }) == "d") { return 42; }
    return 0;
}
function require_regex_text(result: Option[string]): string {
    match (result) {
        Some(value) => { return value; },
        None => { exit(1); },
    }
    return "";
}
`},
	{"replace-groups", `import "std/regex";
import "std/result";
function main(): i32 {
    if (require_regex_text({ match ({ let __checked_regex_0: regex.Regex = match (regex.compile("(\\w+)@(\\w+)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.replace_all_groups("a@b c@d", "$2@$1") }) { Ok(__text) => Some(__text), Err(__error) => { eprint(__error.to_string()); return 98; } } }) == "b@a d@c") { return 42; }
    return 0;
}
function require_regex_text(result: Option[string]): string {
    match (result) {
        Some(value) => { return value; },
        None => { exit(1); },
    }
    return "";
}
`},
	{"named-groups", `import "std/regex";
import "std/result";
function main(): i32 {
    let Some(m) = { let __checked_regex_1: regex.Regex = match (regex.compile("(?<y>\\d+)-(?<m>\\d+)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_1.captures("12-34") } else { return 1; };
    let t: string = require_regex_text({ match ({ let __checked_regex_0: regex.Regex = match (regex.compile("(?<w>\\w+)")) { Ok(__compiled) => __compiled, Err(__error) => { eprint(__error.to_string()); return 98; } }; __checked_regex_0.replace_groups("hi", "[${w}]") }) { Ok(__text) => Some(__text), Err(__error) => { eprint(__error.to_string()); return 98; } } });
    if (require_regex_text({ match (m.group_named("y")) { Ok(__value) => __value, Err(__error) => { eprint(__error.to_string()); return 98; } } }) == "12" && index_is(m.group_index("m"), 2) && t == "[hi]") { return 42; }
    return 0;
}
function index_is(o: Option[i32], want: i32): boolean {
    if let Some(v) = o { return v == want; }
    return false;
}
function require_regex_text(result: Option[string]): string {
    match (result) {
        Some(value) => { return value; },
        None => { exit(1); },
    }
    return "";
}
`},
}

func TestSelfHostRegexModuleIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
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
			if want != 42 {
				t.Fatalf("interpreter exit = %d, want 42", want)
			}
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
