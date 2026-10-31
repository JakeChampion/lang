package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAsyncGatherFileFds(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	for _, p := range []string{fileA, fileB} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write probe %s: %v", p, err)
		}
	}

	src := fmt.Sprintf(`import "std/async";

function woken(fd: i32, wake: async.Wake): async.Future[i32] {
    if let Woke(i) = wake { return Ready(fd); }
    return Ready(0 - 1);
}

function start_io(fd: i32): async.Future[i32] {
    let set: i32[] = [fd, 1];
    return Pending(async.Wait { set: set, until: None }, (wake: async.Wake): async.Future[i32] => woken(fd, wake));
}

function main(): i32 {
    match (open_reader("%s")) {
        Ok(ra) => {
            match (open_reader("%s")) {
                Ok(rb) => {
                    let tasks: async.Future[i32][] = [start_io(ra.fd), start_io(rb.fd)];
                    let results: i32[] = async.block_on(async.gather(tasks)).unwrap_or([]);
                    if (results.len() != 2) { return 90; }
                    if (results[0] < 0) { return 91; }
                    if (results[1] < 0) { return 92; }
                    return 42;
                },
                Err(e) => { return 98; }
            }
        },
        Err(e) => { return 99; }
    }
}`, fileA, fileB)

	runAsyncIOProgram(t, bin, dir, "gather_files", src, 42)
}

func TestAsyncGatherTimers(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	src := `import "std/async";

function start_timer(ms: i32): async.Future[i32] {
    let set: i32[] = [timer_fd(ms), 1];
    return Pending(async.Wait { set: set, until: None }, (wake: async.Wake): async.Future[i32] => Ready(ms));
}

function main(): i32 {
    let tasks: async.Future[i32][] = [start_timer(10), start_timer(15)];
    let results: i32[] = async.block_on(async.gather(tasks)).unwrap_or([]);
    if (results.len() != 2) { return 90; }
    if (results[0] != 10) { return 91; }
    if (results[1] != 15) { return 92; }
    return 42;
}`

	runAsyncIOProgram(t, bin, dir, "gather_timers", src, 42)
}

func TestAsyncRaceTimers(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	src := `import "std/async";

function start_timer(ms: i32): async.Future[i32] {
    let set: i32[] = [timer_fd(ms), 1];
    return Pending(async.Wait { set: set, until: None }, (wake: async.Wake): async.Future[i32] => Ready(ms));
}

function main(): i32 {
    let tasks: async.Future[i32][] = [start_timer(5000), start_timer(10)];
    let won: (i32, i32) = async.block_on(async.race(tasks)).unwrap_or((0 - 1, 0 - 1));
    if (won.1 != 10) { return 91; }
    if (won.0 != 1) { return 92; }
    return 42;
}`

	runAsyncIOProgram(t, bin, dir, "race_timers", src, 42)
}

func TestAsyncWithDeadline(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()

	helper := `import "std/async";
import "std/clock";
import "std/time";

function start_timer(ms: i32): async.Future[i32] {
    let set: i32[] = [timer_fd(ms), 1];
    return Pending(async.Wait { set: set, until: None }, (wake: async.Wake): async.Future[i32] => Ready(ms));
}
`
	cases := []struct {
		name, body string
		want       int
	}{
		{
			name: "completes_in_time",
			body: `function main(): i32 {
    let tasks: async.Future[i32][] = [start_timer(5)];
    let r: Option[i32][] = async.block_on(async.with_deadline(clock.instant().add(time.duration_millis(5000)), tasks)).unwrap_or([None]);
    match (r[0]) { Some(v) => { return v; }, None => { return 99; } }
}`,
			want: 5,
		},
		{
			name: "times_out",
			body: `function main(): i32 {
    let tasks: async.Future[i32][] = [start_timer(5000)];
    let r: Option[i32][] = async.block_on(async.with_deadline(clock.instant().add(time.duration_millis(20)), tasks)).unwrap_or([Some(0)]);
    match (r[0]) { Some(v) => { return 99; }, None => { return 42; } }
}`,
			want: 42,
		},
	}
	for _, tc := range cases {
		tc := tc
		runAsyncIOProgram(t, bin, dir, "deadline_"+tc.name, helper+tc.body, tc.want)
	}
}

func runAsyncIOProgram(t *testing.T, bin, dir, name, src string, want int) {
	t.Helper()
	backends := []struct {
		target string
		qemu   func(*testing.T) string
		run    func(qemu, bin string, args ...string) *exec.Cmd
	}{
		{"x86-64-linux", x86QemuOrEmpty, runX86Bin},
		{"arm64-linux", arm64QemuOrEmpty, runArm64Bin},
	}
	for _, be := range backends {
		be := be
		t.Run(name+"/"+be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			srcPath := filepath.Join(dir, name+"_"+be.target+".fern")
			if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, name+"_"+be.target+".bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s/%s: exit = %d, want %d", name, be.target, code, want)
			}
		})
	}
}
