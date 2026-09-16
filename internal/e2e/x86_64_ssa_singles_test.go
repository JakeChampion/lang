package e2e

import "testing"

// write, __fern_heap_bump_bytes, Reader.read_line and read_dir on
// `-backend ssa -target x86-64-linux`, against the DEFAULT x86-64 backend on
// stdout, stderr and exit status. read_dir's order is getdents64's on both
// sides, so the listing is printed as a count and a sum of name lengths.
var x86SSASingleCases = []struct {
	name string
	src  string
}{
	{
		name: "write_emits_no_newline",
		src: `function main(): i32 {
  write("a");
  write("b");
  write("\n");
  write("");
  write("end\n");
  return 0;
}`,
	},
	{
		// The high-water mark never decreases and moves past an allocation.
		name: "heap_bump_bytes_is_monotone",
		src: `import "std/i32";
function main(): i32 {
  var before: i64 = __heap_bump_bytes();
  var xs: i32[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
  var s: string = "grow" + "ing";
  var after: i64 = __heap_bump_bytes();
  if (after >= before && after > 0) { stdout().write("monotone\n"); } else { return 1; }
  stdout().write(xs.len().to_string() + s.len().to_string() + "\n");
  return 0;
}`,
	},
	{
		// Lines keep their newline, the last line has none, and EOF is None.
		name: "read_line_reads_lines_then_none",
		src: `import "std/i32";
function main(): i32 {
  match (write_file("lines_probe.txt", "first\nsecond line\n\nlast")) {
    Ok(u) => {},
    Err(e) => { return 1; },
  }
  match (open_reader("lines_probe.txt")) {
    Ok(r) => {
      var n: i32 = 0;
      var more: boolean = true;
      while (more) {
        match (r.read_line()) {
          Some(l) => { n = n + 1; stdout().write(n.to_string() + ":[" + l + "]\n"); },
          None => { more = false; },
        }
      }
      stdout().write("lines=" + n.to_string() + "\n");
      r.close();
    },
    Err(e) => { return 2; },
  }
  return 0;
}`,
	},
	{
		name: "read_dir_lists_children_and_reports_a_missing_directory",
		src: `import "std/i32";
function main(): i32 {
  match (temp_dir("fernssa-rd")) {
    Ok(d) => {
      var names: string[] = ["alpha.txt", "b", "gamma-long-name.log"];
      var i: i32 = 0;
      while (i < names.len()) {
        match (write_file(d + "/" + names[i], names[i])) {
          Ok(u) => {},
          Err(e) => { return 1; },
        }
        i = i + 1;
      }
      match (read_dir(d)) {
        Ok(entries) => {
          var total: i32 = 0;
          var j: i32 = 0;
          while (j < entries.len()) { total = total + entries[j].len(); j = j + 1; }
          stdout().write("entries=" + entries.len().to_string() + " chars=" + total.to_string() + "\n");
        },
        Err(e) => { return 2; },
      }
      match (read_dir(d + "/alpha.txt")) {
        Ok(entries) => { return 3; },
        Err(e) => {
          match (e) {
            Other(p, msg) => { stdout().write("not-a-dir:" + msg + "\n"); },
            _ => { return 4; },
          }
        },
      }
    },
    Err(e) => { return 5; },
  }
  match (read_dir("no_such_dir_for_ssa")) {
    Ok(entries) => { return 6; },
    Err(e) => {
      match (e) {
        NotFound(p) => { stdout().write("NotFound:" + p + "\n"); return 0; },
        _ => { return 7; },
      }
    },
  }
}`,
	},
}

func TestX86_64SSASinglesMatchDefaultBackend(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)

	for _, c := range x86SSASingleCases {
		t.Run(c.name, func(t *testing.T) {
			ssaOut, ssaErr, ssaCode := buildRunX86(t, fern, runner, c.src, true)
			flatOut, flatErr, flatCode := buildRunX86(t, fern, runner, c.src, false)

			if ssaCode != flatCode {
				t.Errorf("exit status: ssa=%d flat=%d", ssaCode, flatCode)
			}
			if ssaOut != flatOut {
				t.Errorf("stdout differs:\n ssa=%q\nflat=%q", ssaOut, flatOut)
			}
			if ssaErr != flatErr {
				t.Errorf("stderr differs:\n ssa=%q\nflat=%q", ssaErr, flatErr)
			}
			if ssaOut == "" {
				t.Errorf("no output at all: the program did not reach its prints")
			}
		})
	}
}
