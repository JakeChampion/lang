package e2eharness

import (
	"fmt"
	"io"
	"net"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// AcceptDistributionServerSource is a supervised server whose workers
// answer with an id of their own. Port 0 serves an inherited listener.
func AcceptDistributionServerSource(port, workers int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/i64";
import "std/platform";
function main(): i32 {
    let id: Cell[i64] = cell_new(0 as i64);
    let opts: serve.Config = serve.Config { ...serve.config(), workers: %d, max_connections: 16384, max_connections_per_ip: 0 };
    return serve.exit_code(serve.supervise(%d, opts, (req: HttpRequest, plat: platform.Host): HttpResponse => {
        if (id.get() == (0 as i64)) { id.set(monotonic_ns()); }
        return http.ok(id.get().to_string());
    }));
}
`, workers, port)
}

func MeasureAcceptDistribution(t *testing.T, addr string, held int) map[string]int {
	t.Helper()
	conns := make([]net.Conn, 0, held)
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < held; i++ {
		c, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conns = append(conns, c)
		if _, err := io.WriteString(c, "GET /which HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	per := map[string]int{}
	for i, c := range conns {
		if err := c.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.Fatal(err)
		}
		resp := readResponse(t, c, fmt.Sprintf("connection %d", i))
		body, _ := io.ReadAll(resp.Body)
		per[strings.TrimSpace(string(body))]++
	}
	return per
}

func CheckAcceptDistribution(t *testing.T, addr string, workers, held int) {
	t.Helper()
	WaitServerReady(t, addr)
	per := MeasureAcceptDistribution(t, addr, held)
	counts := make([]int, 0, len(per))
	for _, n := range per {
		counts = append(counts, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(counts)))
	t.Logf("accept distribution: %d connections over %d workers landed on %d of them: %v", held, workers, len(per), counts)
	if len(per) < 2 {
		t.Fatalf("%d connections over %d workers all landed on one worker", held, workers)
	}
}

// CheckAcceptDistributionEven dials the burst from one thread on each of
// the first `workers` CPUs and requires every worker to take at least half
// its share. A listener per worker is steered by the CPU a connection
// arrives on, and on loopback that is the CPU of the thread that dials.
func CheckAcceptDistributionEven(t *testing.T, addr string, workers, held int) {
	t.Helper()
	WaitServerReady(t, addr)
	conns := make([][]net.Conn, workers)
	errs := make(chan error, workers)
	for cpu := 0; cpu < workers; cpu++ {
		go func(cpu int) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			if err := pinToCPU(cpu); err != nil {
				errs <- fmt.Errorf("pin to CPU %d: %v", cpu, err)
				return
			}
			for i := 0; i < held/workers; i++ {
				c, err := net.DialTimeout("tcp", addr, 10*time.Second)
				if err != nil {
					errs <- fmt.Errorf("dial from CPU %d: %v", cpu, err)
					return
				}
				conns[cpu] = append(conns[cpu], c)
				if _, err := io.WriteString(c, "GET /which HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
					errs <- fmt.Errorf("request from CPU %d: %v", cpu, err)
					return
				}
			}
			errs <- nil
		}(cpu)
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	per := map[string]int{}
	for cpu := range conns {
		for i, c := range conns[cpu] {
			defer c.Close()
			if err := c.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
				t.Fatal(err)
			}
			resp := readResponse(t, c, fmt.Sprintf("connection %d from CPU %d", i, cpu))
			body, _ := io.ReadAll(resp.Body)
			per[strings.TrimSpace(string(body))]++
		}
	}
	counts := make([]int, 0, len(per))
	for _, n := range per {
		counts = append(counts, n)
	}
	sort.Ints(counts)
	total := held / workers * workers
	t.Logf("accept distribution: %d connections from %d CPUs over %d workers: %v", total, workers, workers, counts)
	if len(counts) != workers || counts[0] < total/(2*workers) {
		t.Fatalf("%d connections over %d workers landed as %v; each worker must take at least %d", total, workers, counts, total/(2*workers))
	}
}
