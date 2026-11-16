package e2eharness

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/envflag"
)

func Wasmtime(t testing.TB) string {
	t.Helper()
	p, err := exec.LookPath("wasmtime")
	if err == nil {
		return p
	}
	if envflag.On("FERN_REQUIRE_WASMTIME") {
		t.Fatal("wasmtime not on PATH and FERN_REQUIRE_WASMTIME=1: this lane installs it, so a skip here covers nothing")
	}
	t.Skip("wasmtime not on PATH; skipping wasm e2e")
	return ""
}

// WasmtimeServe is a running `wasmtime serve`.
type WasmtimeServe struct {
	// Addr is the loopback address that wasmtime bound.
	Addr string

	cmd    *exec.Cmd
	log    wasmtimeLog
	copied chan struct{}
	stop   sync.Once
}

type wasmtimeLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *wasmtimeLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *wasmtimeLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

const wasmtimeServeBanner = "Serving HTTP on http://"

// StartWasmtimeServe starts `wasmtime serve` for component on a loopback port
// that the kernel picks. It returns after wasmtime reports the address that it
// bound, so no other socket can take the port first. The caller must call Stop.
func StartWasmtimeServe(wasmtime, component string, args ...string) (*WasmtimeServe, error) {
	argv := append([]string{"serve", "--addr", "127.0.0.1:0"}, args...)
	s := &WasmtimeServe{cmd: exec.Command(wasmtime, append(argv, component)...), copied: make(chan struct{})}
	s.cmd.Stdout = &s.log
	stderr, err := s.cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := s.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start wasmtime serve: %w", err)
	}
	bound := make(chan string, 1)
	go func() {
		defer close(s.copied)
		lines := bufio.NewReader(stderr)
		for {
			line, err := lines.ReadString('\n')
			s.log.Write([]byte(line))
			if rest, ok := strings.CutPrefix(line, wasmtimeServeBanner); ok {
				select {
				case bound <- strings.TrimRight(rest, "/\r\n"):
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case s.Addr = <-bound:
		return s, nil
	case <-s.copied:
		return nil, fmt.Errorf("wasmtime serve exited before it listened:\n%s", s.Stop())
	case <-time.After(serverStartBudget):
		return nil, fmt.Errorf("wasmtime serve did not listen within %v:\n%s", serverStartBudget, s.Stop())
	}
}

// Log returns what wasmtime and the component have written so far.
func (s *WasmtimeServe) Log() string {
	return s.log.String()
}

// Stop kills wasmtime and returns all that it and the component wrote. A
// second call only returns the log.
func (s *WasmtimeServe) Stop() string {
	s.stop.Do(func() {
		_ = s.cmd.Process.Kill()
		<-s.copied
		_ = s.cmd.Wait()
	})
	return s.log.String()
}
