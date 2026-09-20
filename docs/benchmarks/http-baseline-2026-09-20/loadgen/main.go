package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18095", "")
	conc := flag.Int("c", 16, "")
	dur := flag.Duration("d", 5*time.Second, "")
	ka := flag.Bool("k", false, "keep-alive")
	flag.Parse()
	var ok, fail int64
	var mu sync.Mutex
	var lats []time.Duration
	deadline := time.Now().Add(*dur)
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]time.Duration, 0, 4096)
			var c net.Conn
			var br *bufio.Reader
			for time.Now().Before(deadline) {
				t0 := time.Now()
				if c == nil {
					var err error
					c, err = net.Dial("tcp", *addr)
					if err != nil {
						atomic.AddInt64(&fail, 1)
						continue
					}
					br = bufio.NewReader(c)
				}
				if *ka {
					fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
				} else {
					fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
				}
				resp, err := http.ReadResponse(br, nil)
				if err != nil {
					atomic.AddInt64(&fail, 1)
					c.Close()
					c = nil
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if !*ka || resp.Close {
					c.Close()
					c = nil
				}
				if resp.StatusCode == 200 {
					atomic.AddInt64(&ok, 1)
					local = append(local, time.Since(t0))
				} else {
					atomic.AddInt64(&fail, 1)
				}
			}
			mu.Lock()
			lats = append(lats, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p := func(q float64) time.Duration {
		if len(lats) == 0 {
			return 0
		}
		return lats[int(float64(len(lats)-1)*q)]
	}
	fmt.Printf("ok=%d fail=%d rps=%.0f p50=%v p99=%v p999=%v\n", ok, fail, float64(ok)/dur.Seconds(), p(0.5), p(0.99), p(0.999))
}
