package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

type stationRunner struct{ fail bool }

func (*stationRunner) LookPath(string) error       { return nil }
func (*stationRunner) Run(string, ...string) error { return errors.New("mutation forbidden") }
func (r *stationRunner) Output(name string, args ...string) (string, error) {
	if r.fail {
		return "", errors.New("probe failed")
	}
	if name != "iw" || len(args) != 4 || args[0] != "dev" || args[1] != "wlan-test" || args[2] != "station" || args[3] != "dump" {
		return "", errors.New("unexpected command")
	}
	return "Station aa:bb:cc:dd:ee:ff (on wlan-test)\n\tsignal: -61 dBm\n\tconnected time: 42 seconds\n", nil
}
func TestStationAPISeparatesFailureAndMeasuredClients(t *testing.T) {
	s := New(Options{DevMode: true, WiFiInterface: "wlan-test"})
	runner := &stationRunner{}
	s.runner = runner
	read := func() map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/wifi/stations", nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	body := read()
	if body["available"] != true || len(body["stations"].([]any)) != 1 {
		t.Fatal(body)
	}
	runner.fail = true
	body = read()
	if body["available"] != false {
		t.Fatal(body)
	}
}
