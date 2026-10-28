package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOriginExactMatch(t *testing.T) {
	s := New(Options{DevMode: true})
	for _, tc := range []struct {
		o    string
		want int
	}{{"http://router.test:8080", 204}, {"http://ROUTER.test:8080", 204}, {"http://router.test:8080.evil.test", 403}, {"http://evil.test/router.test:8080", 403}, {"http://router.test:8080@evil.test", 403}, {"https://router.test:8080", 403}, {"http://router.test:8081", 403}, {"http://router.test:8080/", 403}, {"null", 403}} {
		t.Run(tc.o, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://router.test:8080/api/v1/config", nil)
			r.Header.Set("Origin", tc.o)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			w := httptest.NewRecorder()
			s.wrapCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
}

func TestDefaultRunnerDeadline(t *testing.T) {
	runner := defaultRunner{timeout: 30 * time.Millisecond}
	for _, output := range []bool{false, true} {
		started := time.Now()
		var err error
		if output {
			_, err = runner.Output("sh", "-c", "exec sleep 5")
		} else {
			err = runner.Run("sh", "-c", "exec sleep 5")
		}
		if err == nil {
			t.Fatal("timed-out command reported success")
		}
		if time.Since(started) > time.Second {
			t.Fatal("command exceeded its deadline")
		}
	}
}
