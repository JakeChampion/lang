package e2eharness

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const connCostHeld = 3000

const connCostTicks = 150

const connCostCycles = 2000

const connCostRatio = 1.5

func PerConnectionCostServerSource() string {
	return `import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/count") {
        return http.ok(__heap_alloc_count().to_string());
    }
    return http.ok("hello");
}
function main(): i32 {
    return serve.run(0, serve.Config { ...serve.config(), max_connections: 16384, max_connections_per_ip: 0, keep_alive_requests: 1000000000 }, handle);
}
`
}

type connCost struct {
	request, cycle, allocs float64
}

func RaiseConnCostLimit(t *testing.T) {
	t.Helper()
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
	if lim.Max < 2*connCostHeld+512 {
		t.Skipf("the descriptor limit %d is under the %d the held connections need", lim.Max, 2*connCostHeld+512)
	}
	lim.Cur = lim.Max
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
}

func CheckPerConnectionCostFlat(t *testing.T, pid int, addr string) {
	t.Helper()
	WaitServerReady(t, addr)
	ka, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ka.Close()
	r := bufio.NewReader(ka)
	keepAlive := func() {
		_ = ka.SetDeadline(time.Now().Add(time.Minute))
		if _, err := fmt.Fprint(ka, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		connCostRead(t, r)
	}
	for i := 0; i < 2000; i++ {
		keepAlive()
	}
	for i := 0; i < 500; i++ {
		connCostCycle(t, addr, "/")
	}
	alone := connCostMeasure(t, pid, addr, keepAlive)
	held := make([]net.Conn, 0, connCostHeld)
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	for len(held) < connCostHeld {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("holding connection %d: %v", len(held), err)
		}
		held = append(held, c)
		_ = c.SetDeadline(time.Now().Add(time.Minute))
		if _, err := fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		connCostRead(t, bufio.NewReader(c))
	}
	busy := connCostMeasure(t, pid, addr, keepAlive)
	runtime.KeepAlive(held)
	t.Logf("alone: %.4f ticks/request, %.4f ticks/cycle, %.2f allocations/cycle", alone.request, alone.cycle, alone.allocs)
	t.Logf("%d held: %.4f ticks/request, %.4f ticks/cycle, %.2f allocations/cycle", connCostHeld, busy.request, busy.cycle, busy.allocs)
	if busy.allocs > alone.allocs+1 {
		t.Errorf("a connection cycle allocates %.2f times with %d connections open and %.2f with none: closing or accepting one rebuilds the table", busy.allocs, connCostHeld, alone.allocs)
	}
	if busy.request > alone.request*connCostRatio {
		t.Errorf("a keep-alive request costs %.2f times as much with %d idle connections open: a loop pass walks every open connection", busy.request/alone.request, connCostHeld)
	}
	if busy.cycle > alone.cycle*connCostRatio {
		t.Errorf("a connection cycle costs %.2f times as much with %d idle connections open", busy.cycle/alone.cycle, connCostHeld)
	}
}

func connCostMeasure(t *testing.T, pid int, addr string, keepAlive func()) connCost {
	t.Helper()
	var m connCost
	m.request = connCostPer(t, pid, keepAlive)
	m.cycle = connCostPer(t, pid, func() { connCostCycle(t, addr, "/") })
	a := connCostCount(t, addr)
	b := connCostCount(t, addr)
	for i := 0; i < connCostCycles; i++ {
		connCostCycle(t, addr, "/")
	}
	z := connCostCount(t, addr)
	m.allocs = float64(z-b-(b-a)) / connCostCycles
	return m
}

func connCostPer(t *testing.T, pid int, step func()) float64 {
	t.Helper()
	start := procTicks(t, pid)
	n := 0
	for {
		for i := 0; i < 100; i++ {
			step()
		}
		n += 100
		if d := procTicks(t, pid) - start; d >= connCostTicks {
			return float64(d) / float64(n)
		}
	}
}

func procTicks(t *testing.T, pid int) int64 {
	t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))
	user, _ := strconv.ParseInt(fields[11], 10, 64)
	sys, _ := strconv.ParseInt(fields[12], 10, 64)
	return user + sys
}

func connCostCycle(t *testing.T, addr, path string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Minute))
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", path); err != nil {
		t.Fatal(err)
	}
	return connCostRead(t, bufio.NewReader(c))
}

func connCostCount(t *testing.T, addr string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(connCostCycle(t, addr, "/count"), 10, 64)
	if err != nil {
		t.Fatalf("/count: %v", err)
	}
	return n
}

func connCostRead(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("answered %d", resp.StatusCode)
	}
	return string(body)
}
