package e2eharness

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBumpPerRequestWaitsForDateChange(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rounds      int
		changeAfter int32
		want        int32
	}{
		{"fast requests", 10, 20, 30},
		{"early date change", 100, 2, 100},
		{"date change after warm count", 10, 1, 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := requests.Add(1)
				date, heap := "Mon, 02 Jan 2006 15:04:05 GMT", 100
				if i > tc.changeAfter {
					date, heap = "Mon, 02 Jan 2006 15:04:06 GMT", 200
				}
				w.Header().Set("Date", date)
				fmt.Fprint(w, heap)
			}))
			defer server.Close()
			CheckBumpPerRequest(t, server.Listener.Addr().String(), tc.rounds)
			if got := requests.Load(); got != tc.want {
				t.Fatalf("requests = %d, want %d", got, tc.want)
			}
		})
	}
}
