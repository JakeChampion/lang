package e2eharness

import (
	"os"
	"strings"
	"testing"
)

func ChildEnv(extra ...string) []string {
	return strippedEnv(ProbeEnv(extra...)...)
}

func strippedEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FERN_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

const BoxedProbe = "FERN_SEM_INLINE="

var boxed bool

func BoxedProbes(t testing.TB) {
	t.Helper()
	t.Setenv("FERN_SEM_INLINE", "")
	boxed = true
	t.Cleanup(func() { boxed = false })
}

func ProbeEnv(env ...string) []string {
	if boxed {
		return append(env, BoxedProbe)
	}
	return env
}

const SelfHostVerify = "FERN_IR_VERIFY=quiet"

func SelfHostChildEnv(extra ...string) []string {
	return ChildEnv(append([]string{SelfHostVerify}, extra...)...)
}
