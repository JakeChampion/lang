package platforms

import (
	"fmt"
	"sort"
)

type Descriptor struct {
	Name string

	Description string

	Capabilities []string

	HandlerKinds []string

	Bindings []string

	ISA         string
	Environment string

	Entry EntryShape

	NoBackend bool
}

type EntryShape string

const (
	EntryProcess EntryShape = "process"

	EntryExports EntryShape = "exports"

	EntryReset EntryShape = "reset"
)

func (e EntryShape) OrDefault() EntryShape {
	if e == "" {
		return EntryProcess
	}
	return e
}

type capabilityProfile = []string

var capabilityProfiles = map[string]capabilityProfile{
	"hosted-linux": {"log", "now", "env", "config", "args", "random", "stdin", "stdout", "fs", "fsmode", "tcp", "proc", "arena", "pollfd", "cabi", "userid", "host", "sysinfo", "cwd", "signal", "rlimit", "sched", "fsinfo", "fsnode", "fsowner", "tty", "fssync", "fsrename", "fsdir", "xattr", "syscall", "rawmem", "subprocess", "unix", "reactor", "uring", "ktls"},

	"hosted-darwin": {"log", "now", "env", "config", "args", "random", "stdin", "stdout", "fs", "fsmode", "tcp", "proc", "arena", "pollfd", "cabi", "userid", "host", "sysinfo", "cwd", "signal", "rlimit", "sched", "fsinfo", "fsnode", "fsowner", "tty", "fssync", "fsrename", "fsdir", "xattr", "syscall", "rawmem", "subprocess", "unix", "reactor", "sysctl"},

	"wasi-cli": {"log", "now", "env", "config", "args", "random", "stdin", "stdout", "fs", "tcp", "host", "signal", "reactor"},

	"wasi-proxy": {"log", "now", "config", "random", "fetch"},

	"none": nil,
}

type environment struct {
	profile      string
	handlerKinds []string
	bindings     []string
	entry        EntryShape
}

var environments = map[string]environment{
	"linux":   {profile: "hosted-linux", handlerKinds: []string{"handle", "main"}, entry: EntryProcess},
	"darwin":  {profile: "hosted-darwin", handlerKinds: []string{"handle", "main"}, entry: EntryProcess},
	"android": {profile: "hosted-linux", handlerKinds: []string{"handle", "main"}, entry: EntryProcess},
	"wasi":    {profile: "wasi-cli", handlerKinds: []string{"main", "handle"}, entry: EntryProcess},

	"wasi-http": {
		profile:      "wasi-proxy",
		handlerKinds: []string{"handle"},
		entry:        EntryExports,
		bindings:     nil,
	},

	"freestanding": {profile: "none", entry: EntryExports},
}

var table = map[string]struct {
	isa         string
	environment string
	description string
	noBackend   bool
}{
	"arm64-linux": {
		isa: "arm64", environment: "linux",
		description: "ARM64 Linux ELF (AWS Graviton, Apple Silicon via container; ARMv8.2-A with the crypto extensions).",
	},
	"arm64-darwin": {
		isa: "arm64", environment: "darwin",
		description: "ARM64 macOS Mach-O (native Apple Silicon Macs; no Linux container needed).",
	},
	"arm64-android": {
		isa: "arm64", environment: "android",
		description: "ARM64 Android — Linux ELF as a static position-independent " +
			"executable (ET_DYN, W^X), so it loads at an arbitrary base under " +
			"Android's loader. Same syscalls / AAPCS64 as arm64-linux.",
	},
	"arm64-freestanding": {
		isa: "arm64", environment: "freestanding", noBackend: true,
		description: "ARM64 with no host — no kernel, no syscalls, no process. " +
			"Type-checkable today; no backend emits for it yet (#6510).",
	},
	"x86-64-linux": {
		isa: "x86-64", environment: "linux",
		description: "x86-64 Linux ELF (native exec on x86_64 hosts, qemu-x86_64 elsewhere).",
	},
	"x86-64-freestanding": {
		isa: "x86-64", environment: "freestanding", noBackend: true,
		description: "x86-64 with no host — no kernel, no syscalls, no process. " +
			"Type-checkable today; no backend emits for it yet (#6510).",
	},
	"wasm32-wasi": {
		isa: "wasm32", environment: "wasi",
		description: "WebAssembly Component Model — CLI world (wasi:cli/run + wasi:filesystem + wasi:clocks).",
	},
	"wasm32-wasi-http": {
		isa: "wasm32", environment: "wasi-http",
		description: "WebAssembly Component Model — proxy world (wasi:http/incoming-handler).",
	},
}

func ForTarget(name string) *Descriptor {
	t, ok := table[name]
	if !ok {
		return nil
	}
	env, ok := environments[t.environment]
	if !ok {
		panic("platforms: target " + name + " names unknown environment " + t.environment)
	}
	caps, ok := capabilityProfiles[env.profile]
	if !ok {
		panic("platforms: environment " + t.environment + " names unknown profile " + env.profile)
	}
	return &Descriptor{
		Name:         name,
		Description:  t.description,
		ISA:          t.isa,
		Environment:  t.environment,
		Capabilities: caps,
		HandlerKinds: env.handlerKinds,
		Bindings:     env.bindings,
		Entry:        env.entry,
		NoBackend:    t.noBackend,
	}
}

func Targets() []string {
	out := make([]string, 0, len(table))
	for name := range table {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func HasCapability(target, capability string) bool {
	d := ForTarget(target)
	if d == nil {
		return false
	}
	for _, c := range d.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

func (d *Descriptor) String() string {
	return fmt.Sprintf("%s: %s", d.Name, d.Description)
}
