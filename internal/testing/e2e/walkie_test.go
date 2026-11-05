package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const walkieApp = "examples/android/walkie"

// runWalkieTest builds one test program of the walkie-talkie app for the host
// and requires that its TAP report has no failure.
func runWalkieTest(t *testing.T, name string) {
	t.Helper()
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(t.TempDir(), strings.TrimSuffix(name, ".fern"))
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(walkieApp, name), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

// The session engine: the state machine, the floor control and the
// protocol, with two engines on a simulated link (acceptance test C).
func TestWalkieSessionEngine(t *testing.T) {
	runWalkieTest(t, "session_test.fern")
}

// The secure channel: the key exchange, the short authentication string,
// sealed frames, replays and a third party (acceptance test G).
func TestWalkieSecureChannel(t *testing.T) {
	runWalkieTest(t, "secure_test.fern")
}

// The LAN transport of the first build: the datagram format, the beacons,
// and the key exchange with a resend of each lost message.
// Two nodes of the app on a simulated network that drops and reorders
// datagrams: pairing, confirmation, the floor and the voice frames.
func TestWalkieNodes(t *testing.T) {
	runWalkieTest(t, "node_test.fern")
}

func TestWalkieLanTransport(t *testing.T) {
	runWalkieTest(t, "lan_test.fern")
}

// The resampler between the rate of the device and the codec.
func TestWalkiePcm(t *testing.T) {
	runWalkieTest(t, "pcm_test.fern")
}

// The Opus voice codec: the round trip, the rate and the packet format.
func TestWalkieVoice(t *testing.T) {
	runWalkieTest(t, "voice_test.fern")
}

// The layout of the screen.
func TestWalkieScreen(t *testing.T) {
	runWalkieTest(t, "screen_test.fern")
}

// The rooms of the internet mode: codes, joins, the rate limit, the expiry
// and the relay table.
func TestWalkieRooms(t *testing.T) {
	runWalkieTest(t, "rooms_test.fern")
}

// The internet mode: two nodes in one room, with each datagram through the
// relay of the server, and one node that moves to a new address.
func TestWalkieInternet(t *testing.T) {
	runWalkieTest(t, "internet_test.fern")
}

// The look of the screen: each screen drawn on a frame in memory.
func TestWalkieUi(t *testing.T) {
	runWalkieTest(t, "ui_test.fern")
}
