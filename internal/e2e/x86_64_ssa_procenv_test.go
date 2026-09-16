package e2e

import "testing"

// args(), env(name) and stat(path) on `-backend ssa -target x86-64-linux`:
// the three call targets coreutils/sort.fern was still refused for after the
// handle family landed (#8822), and the two every other coreutil needs. The
// process arguments and environment are captured in _start, which this
// backend did not do before, so a program that never calls either is
// unchanged.
//
// As the handle cases do, each asserts against the DEFAULT x86-64 backend on
// stdout, stderr and exit status together, since both backends see the same
// argv and environment.
var x86SSAProcEnvCases = []struct {
	name string
	src  string
}{
	{
		// The container is memoised, so a second call hands back the same
		// length; the exit status is the argument count itself.
		name: "args_names_the_program_and_is_memoised",
		src: `function main(): i32 {
  var a: string[] = args();
  if (a.len() >= 1 && a[0].len() > 4) { stdout().write("has-name\n"); }
  var b: string[] = args();
  if (b.len() == a.len()) { stdout().write("stable\n"); }
  return a.len();
}`,
	},
	{
		// PATH is set in every environment the test runs under; a name no
		// process sets is None; and a match on the name alone is not enough,
		// since "HOME" must not match "HOMEX=".
		name: "env_finds_a_set_name_and_misses_an_unset_one",
		src: `function main(): i32 {
  match (env("PATH")) {
    Some(v) => { stdout().write("PATH=" + v + "\n"); },
    None => { return 1; },
  }
  match (env("FERN_NO_SUCH_VARIABLE_X")) {
    Some(v) => { return 2; },
    None => { stdout().write("absent\n"); },
  }
  match (env("PAT")) {
    Some(v) => { return 3; },
    None => { stdout().write("prefix-is-not-a-match\n"); },
  }
  return 0;
}`,
	},
	{
		// A regular file's kind and size, a directory's kind, and the
		// NotFound the path helper reports with the path as given.
		name: "stat_reports_kind_size_and_not_found",
		src: `function main(): i32 {
  match (open_writer("stat_probe.txt")) {
    Ok(w) => { w.write("seven!!"); w.close(); },
    Err(e) => { return 1; },
  }
  match (stat("stat_probe.txt")) {
    Ok(s) => {
      if (s.is_file && !s.is_dir && s.size == 7) { stdout().write("file-7\n"); } else { return 2; }
      if (s.nlink == 1) { stdout().write("nlink-1\n"); }
      if (s.mtime > 0) { stdout().write("mtime-set\n"); }
    },
    Err(e) => { return 3; },
  }
  match (stat(".")) {
    Ok(s) => { if (s.is_dir && !s.is_file) { stdout().write("dir\n"); } else { return 4; } },
    Err(e) => { return 5; },
  }
  match (stat("no_such_stat_target.txt")) {
    Ok(s) => { return 6; },
    Err(e) => {
      match (e) {
        NotFound(p) => { stdout().write("NotFound:" + p + "\n"); },
        _ => { return 7; },
      }
    },
  }
  return 0;
}`,
	},
}

func TestX86_64SSAProcEnvMatchesDefaultBackend(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)

	for _, c := range x86SSAProcEnvCases {
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
		})
	}
}
