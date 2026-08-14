// Package testenv builds the environment a test hands to a child process, and
// checks the environment the test itself was started in.
//
// A test that builds a child environment with `append(os.Environ(), …)` inherits
// every FERN_* variable the caller happened to export. That does not usually make
// the test fail — it makes the test unable to fail. With
// FERN_SIZE_TOLERANCE_PERCENT=99 in the shell, the driver-size gate reports zero
// findings and a test asserting on those findings asserts on an empty list; with
// FERN_LEAKCHECK unset where a test assumed it set, the leak census is not
// compiled in and nothing reports the leak. A vacuous test and a passing test are
// byte-identical in the log.
//
// So a child environment is CONSTRUCTED, never inherited: Clean starts from the
// passthrough allowlist below and With adds exactly the variables the test means
// to exercise. Nothing in Vars — the census of everything that changes what a
// compile emits, what an emitted program does, or which number a gate compares
// against — reaches a child unless the test named it.
//
// The other half is the ambient environment of the test process itself: the
// internal/ast compile-mode flags are package-level vars initialised from
// os.Getenv at init, so an exported FERN_SANITIZE=1 changes every in-process
// compile in a package, and a child started with cmd.Env == nil inherits the lot.
// CheckAmbient is the guard for that half, wired into TestMain of the suites it
// matters to.
package testenv

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// AmbientOKVar names the variable that acknowledges a deliberately dirty run.
// Its value is a comma-separated list of census names: each one is accepted in
// the ambient environment by CheckAmbient AND forwarded to children by Clean, so
// a probe like `FERN_STRICT_IR=1 FERN_TEST_AMBIENT_OK=FERN_STRICT_IR go test …`
// reaches the self-host drivers the way it did before this package existed.
const AmbientOKVar = "FERN_TEST_AMBIENT_OK"

// passNames are the ambient variables a child is given verbatim. The list is an
// allowlist rather than a "strip FERN_*" filter so that a variable nobody has
// classified yet cannot reach a child by default.
var passNames = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TERM", "TZ", "LANG",
	"TMPDIR", "TMP", "TEMP",

	// C toolchain: the assemble+link legs shell out to cc/ld, and a cross leg
	// picks its triple up from these.
	"CC", "CXX", "AR", "AS", "LD", "RANLIB", "OBJCOPY", "STRIP",
	"CFLAGS", "CXXFLAGS", "CPPFLAGS", "LDFLAGS",
	"LIBRARY_PATH", "LD_LIBRARY_PATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH",
	"PKG_CONFIG_PATH",

	// macOS toolchain discovery.
	"SDKROOT", "DEVELOPER_DIR", "MACOSX_DEPLOYMENT_TARGET",

	// TLS roots, for a child that fetches (cmd/fern's package fetcher, gh).
	"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE",
}

// passPrefixes are allowlisted families. Each one names a toolchain that fails
// in a way unrelated to the test when its variables go missing, which is worse
// than the leak this package exists to stop.
var passPrefixes = []string{
	"LC_",       // locale
	"NIX_",      // nix passes cc/ld flags this way; without it cc cannot link
	"GO",        // GOROOT/GOPATH/GOCACHE/GOFLAGS for a child that runs `go`
	"CGO_",      //
	"QEMU_",     // QEMU_LD_PREFIX for the arm64 legs
	"WASMTIME_", // wasmtime's cache and home
	"DYLD_",     // the macOS dynamic loader
	"XDG_",      // the self-host modloader reads XDG_CACHE_HOME
}

// Clean returns a child environment holding the passthrough allowlist and
// nothing else, plus any census variable AmbientOKVar acknowledges.
func Clean() []string {
	return cleanFrom(os.Environ())
}

// With returns Clean plus kv, each entry "NAME=VALUE". An entry replaces any
// same-named entry Clean supplied rather than shadowing it, so the result holds
// exactly one binding per name and no duplicate-key resolution rule is in play.
//
// Panics on an entry without '=' or with an empty name: that is a typo in the
// test, and a typo'd knob is the vacuity this package exists to prevent.
func With(kv ...string) []string {
	return set(Clean(), kv...)
}

// Without returns Clean with `names` removed — for a test whose assertion is
// that the variable is absent, when AmbientOKVar might otherwise forward it.
func Without(names ...string) []string {
	env := Clean()
	out := env[:0:0]
	for _, e := range env {
		if !named(e, names) {
			out = append(out, e)
		}
	}
	return out
}

// CheckAmbient reports the Semantic census variables set in the ambient
// environment and not acknowledged via AmbientOKVar. A non-nil error means the
// suite would be testing a compiler configuration it does not name.
func CheckAmbient() error {
	return checkAmbient(os.Environ())
}

// MustCheckAmbient fails the process when CheckAmbient does. Call it from
// TestMain: a package whose in-process compile flags come from the ambient
// environment cannot report a trustworthy result, so refusing to run is the
// honest outcome.
func MustCheckAmbient() {
	if err := CheckAmbient(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func cleanFrom(environ []string) []string {
	ok := acknowledged(environ)
	var out []string
	for _, e := range environ {
		name, _, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		if ok[name] && Lookup(name) != nil {
			out = append(out, e)
			continue
		}
		if passes(name) {
			out = append(out, e)
		}
	}
	return out
}

func checkAmbient(environ []string) error {
	ok := acknowledged(environ)
	var bad []string
	for _, e := range environ {
		name, _, found := strings.Cut(e, "=")
		if !found || ok[name] {
			continue
		}
		if v := Lookup(name); v != nil && v.Class == Semantic {
			bad = append(bad, name)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	var b strings.Builder
	fmt.Fprintf(&b, "ambient environment sets %d behaviour-changing variable(s), so this suite would test a compiler configuration it does not name:\n", len(bad))
	for _, name := range bad {
		fmt.Fprintf(&b, "  %s — %s\n", name, Lookup(name).Effect)
	}
	fmt.Fprintf(&b, "Unset them, or name the ones you meant in %s=%s to accept AND forward them.",
		AmbientOKVar, strings.Join(bad, ","))
	return fmt.Errorf("%s", b.String())
}

func acknowledged(environ []string) map[string]bool {
	ok := map[string]bool{}
	for _, e := range environ {
		name, val, found := strings.Cut(e, "=")
		if !found || name != AmbientOKVar {
			continue
		}
		for _, n := range strings.Split(val, ",") {
			if n = strings.TrimSpace(n); n != "" {
				ok[n] = true
			}
		}
	}
	return ok
}

func passes(name string) bool {
	for _, n := range passNames {
		if name == n {
			return true
		}
	}
	for _, p := range passPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func set(env []string, kv ...string) []string {
	for _, e := range kv {
		name, _, found := strings.Cut(e, "=")
		if !found || name == "" {
			panic("testenv: environment entry " + fmt.Sprintf("%q", e) + ` is not "NAME=VALUE"`)
		}
		out := env[:0]
		for _, cur := range env {
			if !named(cur, []string{name}) {
				out = append(out, cur)
			}
		}
		env = append(out, e)
	}
	return env
}

func named(entry string, names []string) bool {
	name, _, found := strings.Cut(entry, "=")
	if !found {
		return false
	}
	for _, n := range names {
		if name == n {
			return true
		}
	}
	return false
}
