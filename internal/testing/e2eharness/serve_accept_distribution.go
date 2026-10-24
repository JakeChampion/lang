package e2eharness

import (
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"testing"
	"time"
)

func AcceptDistributionServerSource(workers int) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/i64";
import "std/platform";
function main(): i32 {
    let id: Cell[i64] = cell_new(0 as i64);
    let opts: serve.Config = serve.Config { ...serve.config(), workers: %d, max_connections: 16384, max_connections_per_ip: 0 };
    return serve.supervise(0, opts, (req: HttpRequest, plat: platform.Host): HttpResponse => {
        if (id.get() == (0 as i64)) { id.set(monotonic_ns()); }
        return http.ok(id.get().to_string());
    });
}
`, workers)
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
	WaitServerReady(t, addr, 10*time.Second)
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
