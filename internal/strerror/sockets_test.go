package strerror

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

func TestWasiSocketErrorTableParity(t *testing.T) {
	wit, err := os.ReadFile("../../cmd/fern/wit/deps/sockets/network.wit")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)enum error-code\s*\{([^}]+)\}`).FindSubmatch(wit)
	if len(m) != 2 {
		t.Fatal("socket error-code enum missing from vendored WIT")
	}
	var names []string
	for _, name := range strings.Split(string(m[1]), ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	if !reflect.DeepEqual(names, component.WasiSocketsNetworkErrorCodeNames) {
		t.Fatal("component socket error-code discriminants differ from WIT")
	}
	if len(names) != len(WasiSocketErrorCodes) {
		t.Fatal("socket errno map does not cover the WIT enum")
	}
	fern := fernInts(t, readSelfHost(t), "wasi_socket_error_errnos")
	if len(fern) != len(names) {
		t.Fatal("self-host socket errno map has the wrong length")
	}
	for i, ec := range WasiSocketErrorCodes {
		if ec.Code != names[i] {
			t.Errorf("code %d: %s, want %s", i, ec.Code, names[i])
		}
		want := Number(Wasi, ec.Errno)
		if want <= 0 || WasiSocketErrno(i) != want || fern[i] != want {
			t.Errorf("%s: errno %d, self-host %d, want positive %s=%d", ec.Code, WasiSocketErrno(i), fern[i], ec.Errno, want)
		}
	}
	for _, code := range []int{-1, len(names), 255} {
		if got := WasiSocketErrno(code); got != Number(Wasi, "EIO") {
			t.Errorf("unknown code %d: errno %d, want EIO", code, got)
		}
	}
}
