package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostTaskPortable(t *testing.T) {
	checkTaskProgramOnTargets(t, e2eharness.TaskPortableProgram, e2eharness.TaskPortableWant)
}

// TestSelfHostTaskDeadline parks with a deadline beyond 32-bit milliseconds
// and reads it back through the runtime on each target.
func TestSelfHostTaskDeadline(t *testing.T) {
	checkTaskProgramOnTargets(t, e2eharness.TaskDeadlineProgram, e2eharness.TaskDeadlineWant)
}

// A parked task whose last handle goes away is cancelled: its wait answers
// Cancelled and its defer runs. A task that was not started prints nothing.
// A task whose handle lives is not cancelled.
const taskDroppedProgram = `import "std/async";

function body(name: string): i32 {
    defer print("defer " + name);
    let held = "live " + name;
    print("start " + name);
    match (async.wait_any([], None)) {
        Woke(_) => { print("woke " + name + " " + held); },
        TimedOut => { print("timed out " + name + " " + held); },
        Cancelled => { print("cancelled " + name + " " + held); }
    }
    return 1;
}

@noinline
function started(): void {
    let t = async.task_new((): i32 => body("started"));
    match (async.task_start(t)) {
        Done(_) => { print("done"); },
        Suspended(_) => { print("suspended"); }
    }
}

@noinline
function made(): void {
    let t = async.task_new((): i32 => body("made"));
    print("made");
}

@noinline
function spawned(): void {
    match (async.spawn((): i32 => body("spawned"))) {
        Ready(_) => { print("ready"); },
        Pending(_, _) => { print("pending"); },
        Cancelled => { print("cancelled"); }
    }
}

@noinline
function park(t: async.Task[i32]): void {
    match (async.task_start(t)) {
        Done(_) => { print("done"); },
        Suspended(_) => { print("suspended"); }
    }
}

@noinline
function kept(): void {
    let t = async.task_new((): i32 => body("kept"));
    park(t);
    print("the handle lives");
    match (async.task_resume(t, async.Wake.TimedOut)) {
        Done(v) => { print("done " + v.to_string()); },
        Suspended(_) => { print("suspended"); }
    }
}

function main(): i32 {
    started();
    print("--");
    made();
    print("--");
    spawned();
    print("--");
    kept();
    print("end of main");
    return 0;
}
`

const taskDroppedWant = `start started
cancelled started live started
defer started
suspended
--
made
--
start spawned
cancelled spawned live spawned
defer spawned
pending
--
start kept
suspended
the handle lives
timed out kept live kept
defer kept
done 1
end of main
`

func TestSelfHostTaskDroppedWhileParkedIsCancelled(t *testing.T) {
	checkTaskProgramOnTargets(t, taskDroppedProgram, taskDroppedWant)
}

// A parked call that the program drops is cancelled, as a task is. A call
// that the program resumes or cancels ends one time.
const callDroppedProgram = `import "std/async";

function body(name: string, n: i32): i32 {
    defer print("defer " + name);
    let held = "live " + name;
    print("start " + name);
    match (async.wait_any([], None)) {
        Woke(_) => { print("woke " + name + " " + held); },
        TimedOut => { print("timed out " + name + " " + held); },
        Cancelled => { print("cancelled " + name + " " + held); }
    }
    return n;
}

@noinline
function dropped(): void {
    match (async.call_start(body, "dropped", 1)) {
        Returned(_) => { print("returned"); },
        Parked(_, _) => { print("parked"); }
    }
}

@noinline
function resumed(): void {
    match (async.call_start(body, "resumed", 2)) {
        Returned(_) => { print("returned"); },
        Parked(call, _) => {
            print("parked");
            match (async.call_resume(call, async.Wake.TimedOut)) {
                Done(v) => { print("done " + v.to_string()); },
                Suspended(_) => { print("suspended"); }
            }
        }
    }
}

@noinline
function cancelled(): void {
    match (async.call_start(body, "cancelled", 3)) {
        Returned(_) => { print("returned"); },
        Parked(call, _) => {
            print("parked");
            async.call_cancel(call);
            print("after cancel");
        }
    }
}

function main(): i32 {
    dropped();
    print("--");
    resumed();
    print("--");
    cancelled();
    print("end of main");
    return 0;
}
`

const callDroppedWant = `start dropped
cancelled dropped live dropped
defer dropped
parked
--
start resumed
parked
timed out resumed live resumed
defer resumed
done 2
--
start cancelled
parked
cancelled cancelled live cancelled
defer cancelled
after cancel
end of main
`

func TestSelfHostCallDroppedWhileParkedIsCancelled(t *testing.T) {
	checkTaskProgramOnTargets(t, callDroppedProgram, callDroppedWant)
}

// checkTaskProgramOnTargets runs program on x86-64-linux, arm64-linux and
// wasm32-wasi (composed against cmd/fern/wit) and compares its stdout with want.
func checkTaskProgramOnTargets(t *testing.T, program, want string) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
				if len(cli.runner) == 0 {
					cmd = exec.Command(bin)
				} else {
					cmd = exec.Command(cli.runner[0], append(append([]string{}, cli.runner[1:]...), bin)...)
				}
			case "arm64-linux":
				armgcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target, "FERN_STRICT_IR=1"))
				if err != nil {
					t.Fatal(err)
				}
				cmd = runArm64Bin(qemu, buildBinArm64(t, armgcc, filepath.Dir(src), "prog", string(asm)))
			case "wasm32-wasi":
				wasmtime, err := exec.LookPath("wasmtime")
				if err != nil {
					t.Skip("wasmtime not on PATH")
				}
				wasmtools, err := exec.LookPath("wasm-tools")
				if err != nil {
					t.Skip("wasm-tools not on PATH")
				}
				adapter := os.Getenv("FERN_WASI_ADAPTER")
				if adapter == "" {
					t.Skip("FERN_WASI_ADAPTER unset")
				}
				wat := cli.emit(t, src, target, "FERN_STRICT_IR=1")
				core := filepath.Join(filepath.Dir(wat), "prog.core.wasm")
				if out, err := exec.Command(wasmtools, "parse", wat, "-o", core).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools parse: %v\n%s", err, out)
				}
				embedded := filepath.Join(filepath.Dir(wat), "prog.embedded.wasm")
				if out, err := exec.Command(wasmtools, "component", "embed", filepath.Join(repoRootFromTest(t), "cmd", "fern", "wit"),
					"-w", "fern", core, "-o", embedded).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
				}
				comp := filepath.Join(filepath.Dir(wat), "prog.component.wasm")
				if out, err := exec.Command(wasmtools, "component", "new", embedded,
					"--adapt", "wasi_snapshot_preview1="+adapter, "-o", comp).CombinedOutput(); err != nil {
					t.Fatalf("wasm-tools component new: %v\n%s", err, out)
				}
				cmd = exec.Command(wasmtime, "run", comp)
			}
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errb.String())
			}
			if out.String() != want {
				t.Fatalf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", out.String(), want, errb.String())
			}
		})
	}
}
