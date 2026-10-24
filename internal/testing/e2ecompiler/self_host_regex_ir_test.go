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
function main(): i32 { if (regex.regex_match("a|b", "b")) { return 42; } return 0; }`},
	{"alt-nomatch", `import "std/regex";
function main(): i32 { if (!regex.regex_match("a|b", "c")) { return 42; } return 0; }`},
	{"alt-of-seq", `import "std/regex";
function main(): i32 { if (regex.regex_match("(ab|cd)", "xxcdyy")) { return 42; } return 0; }`},
	{"anchored-alt", `import "std/regex";
function main(): i32 {
    if (!regex.regex_match("^(cat|dog|bird)$", "dog")) { return 0; }
    if (!regex.regex_match("(foo|barbaz)qux", "aaaa barbazqux")) { return 0; }
    return 42;
}`},
	{"class-plus", `import "std/regex";
function main(): i32 { if (regex.regex_match("[abc]+", "cab")) { return 42; } return 0; }`},
	{"group-plus", `import "std/regex";
function main(): i32 { if (regex.regex_match("(ab)+c", "ababc")) { return 42; } return 0; }`},
	{"captures", `import "std/regex";
function main(): i32 {
    let Some(m) = regex.regex_captures("(\\d+)-(\\d+)", "order 123-456 shipped") else { return 1; };
    if (require_regex_text(m.group(1)) == "123" && require_regex_text(m.group(2)) == "456" && m.group_count() == 2) { return 42; }
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
function main(): i32 {
    let Some(nc) = regex.regex_captures("(?:ab)+(c)", "ababc") else { return 1; };
    let all: regex.RCaps[] = regex.regex_captures_all("(\\w+)@(\\w+)", "a@b c@d");
    if (nc.group_count() == 1 && require_regex_text(nc.group(1)) == "c" && all.len() == 2 && require_regex_text(all[1].group(2)) == "d") { return 42; }
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
function main(): i32 {
    if (require_regex_text(regex.regex_replace_all_groups("(\\w+)@(\\w+)", "a@b c@d", "$2@$1")) == "b@a d@c") { return 42; }
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
function main(): i32 {
    let Some(m) = regex.regex_captures("(?<y>\\d+)-(?<m>\\d+)", "12-34") else { return 1; };
    let t: string = require_regex_text(regex.regex_replace_groups("(?<w>\\w+)", "hi", "[${w}]"));
    if (require_regex_text(m.group_named("y")) == "12" && index_is(m.group_index("m"), 2) && t == "[hi]") { return 42; }
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
