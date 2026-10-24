package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "server address")
	conns := flag.Int("c", 64, "connections")
	depth := flag.Int("depth", 16, "requests per batch")
	dur := flag.Duration("d", 10*time.Second, "how long to load")
	flag.Parse()

	batch := bytes.Repeat([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"), *depth)
	var done, failed atomic.Int64
	var started sync.WaitGroup
	stop := make(chan struct{})
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < *conns; i++ {
		wg.Add(1)
		started.Add(1)
		go func() {
			defer wg.Done()
			first := true
			for {
				c, err := net.DialTimeout("tcp", *addr, 10*time.Second)
				if first {
					started.Done()
					first = false
				}
				if err != nil {
					failed.Add(1)
					return
				}
				closed, err := pipeline(c, batch, *depth, &done, stop)
				c.Close()
				if err != nil {
					failed.Add(1)
					return
				}
				if !closed {
					return
				}
			}
		}()
	}
	started.Wait()
	time.Sleep(*dur - time.Since(start))
	close(stop)
	elapsed := time.Since(start)
	n := done.Load()
	wg.Wait()
	fmt.Printf("rps %d failed %d\n", int64(float64(n)/elapsed.Seconds()), failed.Load())
}

func pipeline(c net.Conn, batch []byte, depth int, done *atomic.Int64, stop chan struct{}) (bool, error) {
	r := bufio.NewReaderSize(c, 64<<10)
	for {
		select {
		case <-stop:
			return false, nil
		default:
		}
		c.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := c.Write(batch); err != nil {
			return true, nil
		}
		for j := 0; j < depth; j++ {
			if err := readResponse(r); err == io.EOF {
				return true, nil
			} else if err != nil {
				return false, err
			}
			done.Add(1)
		}
	}
}

func readResponse(r *bufio.Reader) error {
	length := -1
	line, err := r.ReadSlice('\n')
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(line, []byte("HTTP/1.1 200")) {
		return fmt.Errorf("status %q", line)
	}
	for {
		line, err = r.ReadSlice('\n')
		if err != nil {
			return err
		}
		if len(line) <= 2 {
			break
		}
		if k, v, ok := bytes.Cut(line, []byte(":")); ok && bytes.EqualFold(bytes.TrimSpace(k), []byte("content-length")) {
			if length, err = strconv.Atoi(string(bytes.TrimSpace(v))); err != nil {
				return err
			}
		}
	}
	if length < 0 {
		return fmt.Errorf("no content-length")
	}
	_, err = io.CopyN(io.Discard, r, int64(length))
	return err
}
