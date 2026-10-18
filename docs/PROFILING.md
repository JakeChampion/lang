# Profiling native programs

Compile a native executable with CPU sampling enabled:

```sh
FERN_PROFILE=cpu fern -o program program.fern
./program 2>cpu.folded
rg -v '^#' cpu.folded | flamegraph.pl >cpu.svg
```

The compiler includes function symbols and preserves generated frame pointers.
The executable needs no profiler library or external symbolizer. Both x86-64
and ARM64 Linux are supported, including ARM64 `-harden` builds, together with
ARM64 Darwin. Android, WASI and shared libraries are refused. An unset
`FERN_PROFILE`, or one set to an off value (the rule in "Environment variables"
in `site/src/content/docs/reference/tooling.md`), leaves profiling disabled; any
other value that is not a mode is refused. The variable controls compilation,
not execution.

The runtime samples process CPU time at 100 Hz using `SIGPROF` and `ITIMER_PROF`.
Each collapsed-stack row contains the root-to-leaf function symbols and a weight
of one. Unknown addresses appear in hexadecimal. A leading comment reports the
number of stored and dropped samples. Redirected stderr also contains any
ordinary diagnostics the program writes.

Sampling uses a fixed buffer of 8,192 stacks, each holding at most 64 frames.
After the buffer fills, reservoir sampling selects which stacks to retain across
the whole execution. The dropped count includes samples not retained, including
ones replaced by later samples. Contending signal handlers drop their sample
rather than wait.
The signal handler allocates no memory and performs no file I/O. Symbolization
and output happen when the program exits normally, calls `exit`, or follows a
runtime fatal-error exit. Failed output preserves the program's exit status.
Termination by an external signal or `SIGKILL` cannot flush a profile.

Frame walking follows the interrupted thread's frame-pointer chain. Handwritten
runtime helpers without frame records can omit callers. Keep the executable's
static symbol table: stripping it before execution leaves hexadecimal addresses.
Externally assembling/linking emitted assembly may also lose function types and
sizes; the compiler's native executable output retains them. Forked children do
not emit a duplicate of the parent's inherited profile; exec a profiled program
to start another independent profile.

CPU sampling owns `SIGPROF` and `ITIMER_PROF`; a program must not replace them
while profiling.

## Allocation profiles

Use `FERN_PROFILE=alloc` when compiling to collect allocation stacks instead of
CPU samples. The same native targets, frame pointers, symbolization and exit
behavior apply. Allocation mode does not install a signal handler or CPU timer.

Every 64th allocation records a stack. Each row's weight is that allocation's
size, rounded to eight bytes, multiplied by 64. Flamegraph widths therefore
compare allocated bytes rather than time. Each recorded row also represents
one sampled object. Before reservoir saturation it stands for approximately 64
allocations. The leading comment
identifies the interval and byte units. Allocator metadata is included in the
rounded size; unused size-class capacity is excluded.

Both fresh blocks and freelist reuse are sampled. Growing a buffer within its
existing allocation does not create another allocation event. Sampling is
systematic rather than random: periodic allocation patterns can bias the byte
estimate. A reservoir retains at most 8,192 allocation stacks across the whole
execution. Its dropped count includes sampled events not retained. Contention
can drop selected events before reservoir selection. After saturation, weights
describe the retained sample rather than total allocated bytes.

## pprof files

Use `cpu-pprof` or `alloc-pprof` and provide an output filename when compiling:

```sh
FERN_PROFILE=cpu-pprof FERN_PROFILE_FILE=cpu.pprof fern -o program program.fern
./program
go tool pprof -top cpu.pprof
go tool pprof -http=localhost:8080 cpu.pprof
```

For allocation profiles, use `FERN_PROFILE=alloc-pprof`. The file contains both
`alloc_space` (bytes, the default view) and `alloc_objects` (allocation counts):

```sh
go tool pprof -top -sample_index=alloc_objects alloc.pprof
```

The compiler embeds `FERN_PROFILE_FILE`; relative paths resolve in the program's
working directory. At exit the runtime creates or truncates that file. Ordinary
program diagnostics remain on stderr. An output failure preserves the exit
status. The runtime writes uncompressed pprof protobuf directly, with no protobuf
or compression library dependency. `go tool pprof` reads it without conversion.

CPU profiles expose sample counts and nanoseconds; allocation profiles expose
object counts and rounded bytes. After reservoir saturation, pprof weights are
scaled to estimate the observations represented by retained samples. Its comment
records retained and dropped counts. Contention losses and systematic allocation
sampling can still bias those estimates.

On Darwin the profiler reads symbols from the running Mach-O image, accounting
for ASLR and the virtual-memory gap occupied by zero-initialized data. It uses
the native signal trampoline and signal-context layout for CPU sampling.

WASI allocation profiles remain a follow-up tracked by #5547.
