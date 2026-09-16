package e2e

import "testing"

// The whole-file helpers (read_file, read_file_bytes, write_file, remove_file,
// temp_dir), the clock helpers (monotonic_ns, now_unix_ms, sleep_ms) and the
// random helpers (random_bytes, random_i32) on
// `-backend ssa -target x86-64-linux`, against the DEFAULT x86-64 backend on
// stdout, stderr and exit status. Every program prints what it observed
// rather than a value that differs per run: a temp directory's name and a
// clock reading are compared as properties.
var x86SSAFileCases = []struct {
	name string
	src  string
}{
	{
		// A round trip through multibyte text, then a second write that
		// truncates rather than appends.
		name: "write_file_then_read_file_round_trips_and_truncates",
		src: `import "std/i32";
function main(): i32 {
  match (write_file("files_rt.txt", "héllo ✓ world\nsecond line\n")) {
    Ok(u) => {},
    Err(e) => { return 1; },
  }
  match (read_file("files_rt.txt")) {
    Ok(s) => { stdout().write("len=" + s.len().to_string() + "\n" + s); },
    Err(e) => { return 2; },
  }
  match (write_file("files_rt.txt", "short")) {
    Ok(u) => {},
    Err(e) => { return 3; },
  }
  match (read_file("files_rt.txt")) {
    Ok(s) => { stdout().write("[" + s + "]\n"); return s.len(); },
    Err(e) => { return 4; },
  }
}`,
	},
	{
		name: "read_file_missing_path_is_not_found_with_the_path",
		src: `function main(): i32 {
  match (read_file("no_such_file_for_ssa.txt")) {
    Ok(s) => { return 1; },
    Err(e) => {
      match (e) {
        NotFound(p) => { stdout().write("NotFound:" + p + "\n"); return 0; },
        _ => { stdout().write("other\n"); return 2; },
      }
    },
  }
}`,
	},
	{
		// Invalid UTF-8 is Err(InvalidUtf8) from read_file and plain bytes
		// from read_file_bytes; remove_file makes the path NotFound for
		// both, and a second remove reports it.
		name: "invalid_utf8_bytes_and_remove_file",
		src: `import "std/i32";
function main(): i32 {
  var raw: u8[] = [104 as u8, 105 as u8, 255 as u8, 254 as u8];
  match (write_file("files_raw.bin", string_from_bytes_unchecked(raw))) {
    Ok(u) => {},
    Err(e) => { return 1; },
  }
  match (read_file("files_raw.bin")) {
    Ok(s) => { stdout().write("text?!\n"); return 2; },
    Err(e) => {
      match (e) {
        InvalidUtf8(p) => { stdout().write("InvalidUtf8:" + p + "\n"); },
        _ => { stdout().write("other\n"); return 3; },
      }
    },
  }
  match (read_file_bytes("files_raw.bin")) {
    Ok(b) => {
      stdout().write("bytes=" + b.len().to_string() + " last=" + (b[3] as i32).to_string() + " first=" + (b[0] as i32).to_string() + "\n");
    },
    Err(e) => { return 4; },
  }
  match (remove_file("files_raw.bin")) {
    Ok(u) => { stdout().write("removed\n"); },
    Err(e) => { return 5; },
  }
  match (read_file_bytes("files_raw.bin")) {
    Ok(b) => { return 6; },
    Err(e) => {
      match (e) {
        NotFound(p) => { stdout().write("gone:" + p + "\n"); },
        _ => { return 7; },
      }
    },
  }
  match (remove_file("files_raw.bin")) {
    Ok(u) => { return 8; },
    Err(e) => {
      match (e) {
        NotFound(p) => { stdout().write("remove-gone:" + p + "\n"); return 0; },
        _ => { return 9; },
      }
    },
  }
}`,
	},
	{
		// Reading through a hint that is short of the content: /proc files
		// report st_size 0 and generate their bytes on the read.
		name: "read_file_grows_past_a_short_size_hint",
		src: `import "std/i32";
function main(): i32 {
  match (read_file("/proc/self/status")) {
    Ok(s) => {
      if (s.len() > 100) { stdout().write("proc-read\n"); return 0; }
      stdout().write("short:" + s.len().to_string() + "\n");
      return 1;
    },
    Err(e) => { return 2; },
  }
}`,
	},
	{
		// The directory exists and carries the prefix; a slash in the prefix
		// is refused before any syscall, with strerror's text for EINVAL.
		name: "temp_dir_creates_a_directory_and_refuses_a_slash",
		src: `import "std/string";
function main(): i32 {
  match (temp_dir("fernssa")) {
    Ok(p) => {
      if (p.starts_with("/tmp/fernssa-")) { stdout().write("prefixed\n"); } else { stdout().write("not-prefixed:" + p + "\n"); return 1; }
      match (stat(p)) {
        Ok(s) => { if (s.is_dir) { stdout().write("is-dir\n"); } else { return 2; } },
        Err(e) => { return 3; },
      }
      match (write_file(p + "/inner.txt", "inside")) {
        Ok(u) => {},
        Err(e) => { return 4; },
      }
      match (read_file(p + "/inner.txt")) {
        Ok(s) => { stdout().write(s + "\n"); },
        Err(e) => { return 5; },
      }
    },
    Err(e) => { return 6; },
  }
  match (temp_dir("a/b")) {
    Ok(p) => { return 7; },
    Err(e) => {
      match (e) {
        Other(p, msg) => { stdout().write("refused:" + p + ":" + msg + "\n"); return 0; },
        _ => { return 8; },
      }
    },
  }
}`,
	},
	{
		// Clock readings as properties: monotonic never decreases and
		// advances across a sleep by at least most of it, the wall clock is
		// past 2023 in milliseconds, and a non-positive sleep returns.
		name: "clocks_advance_and_sleep_ms_waits",
		src: `function main(): i32 {
  var a: i64 = monotonic_ns();
  sleep_ms(30);
  var b: i64 = monotonic_ns();
  if (b >= a + 25000000) { stdout().write("slept\n"); } else { stdout().write("too-fast\n"); return 1; }
  var c: i64 = monotonic_ns();
  sleep_ms(0);
  sleep_ms(0 - 5);
  var d: i64 = monotonic_ns();
  if (d >= c && d < c + 1000000000) { stdout().write("prompt\n"); } else { return 2; }
  var ms: i64 = now_unix_ms();
  if (ms > 1700000000000 && ms < 4000000000000) { stdout().write("epoch-ms\n"); } else { return 3; }
  return 0;
}`,
	},
	{
		// Randomness as properties: the container has the asked-for length,
		// two draws of 64 bytes differ, and so do two i32 draws.
		name: "random_bytes_and_random_i32_fill_and_vary",
		src: `import "std/i32";
function main(): i32 {
  var a: u8[] = random_bytes(64);
  var b: u8[] = random_bytes(64);
  var empty: u8[] = random_bytes(0);
  stdout().write("len=" + a.len().to_string() + " empty=" + empty.len().to_string() + "\n");
  var same: i32 = 0;
  var i: i32 = 0;
  while (i < 64) { if (a[i] == b[i]) { same = same + 1; } i = i + 1; }
  if (same < 64) { stdout().write("draws-differ\n"); } else { return 1; }
  var x: i32 = random_i32();
  var y: i32 = random_i32();
  var z: i32 = random_i32();
  if (x != y || y != z) { stdout().write("i32-draws-differ\n"); } else { return 2; }
  return 0;
}`,
	},
}

func TestX86_64SSAFilesMatchDefaultBackend(t *testing.T) {
	runner, ok := x86Runner()
	if !ok {
		t.Skip("no way to run x86-64 binaries on this host")
	}
	fern := buildFernCLI(t)

	for _, c := range x86SSAFileCases {
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
