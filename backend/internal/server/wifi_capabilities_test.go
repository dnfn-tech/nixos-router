package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type wifiProbeRunner struct {
	calls []string
	fail  bool
}

func (*wifiProbeRunner) LookPath(string) error       { return nil }
func (*wifiProbeRunner) Run(string, ...string) error { return errors.New("mutation forbidden") }
func (f *wifiProbeRunner) Output(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.fail {
		return "", errors.New("probe failed")
	}
	if reflect.DeepEqual(args, []string{"dev", "wlan-test", "info"}) {
		return "Interface wlan-test\n\twiphy 7\n", nil
	}
	if reflect.DeepEqual(args, []string{"phy", "phy7", "info"}) {
		return "Wiphy phy7\n\tSupported interface modes:\n\t\t * managed\n\t\t * AP\n\tBand 1:\n\t\tFrequencies:\n\t\t\t* 2412 MHz [1] (20.0 dBm)\n\tBand 2:\n\t\tFrequencies:\n\t\t\t* 5180 MHz [36] (disabled)\n", nil
	}
	return "", errors.New("unexpected command")
}
func TestManagedWifiCapabilitiesProbe(t *testing.T) {
	s := New(Options{DevMode: true})
	s.managedRuntime = &runtimeStub{}
	s.wifiInterface = "wlan-test"
	f := &wifiProbeRunner{}
	s.runner = f
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/capabilities/wifi", nil))
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["source"] != "iw" || body["available"] != true || body["maxAP"] != float64(1) || body["phy"] != "phy7" {
		t.Fatalf("unexpected caps: %s", w.Body.String())
	}
	if !reflect.DeepEqual(f.calls, []string{"iw dev wlan-test info", "iw phy phy7 info"}) {
		t.Fatal(f.calls)
	}
	channels, ok := body["channels"].([]interface{})
	if !ok || len(channels) != 2 {
		t.Fatal(body)
	}
	if channels[1].(map[string]interface{})["disabled"] != true {
		t.Fatal("disabled channel presented as usable")
	}
}
func TestWifiCapabilitiesUnknownIsExplicit(t *testing.T) {
	for _, managed := range []bool{false, true} {
		s := New(Options{DevMode: true})
		s.runner = &wifiProbeRunner{fail: true}
		if managed {
			s.managedRuntime = &runtimeStub{}
			s.wifiInterface = "wlan-test"
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/capabilities/wifi", nil))
		var body map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &body)
		if body["available"] != false || body["source"] == "iw" {
			t.Fatalf("false hardware claim: %s", w.Body.String())
		}
		if managed && body["maxAP"] != float64(0) {
			t.Fatalf("unknown max AP: %s", w.Body.String())
		}
	}
}
