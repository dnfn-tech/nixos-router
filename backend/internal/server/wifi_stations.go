package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
	"github.com/dnfn-tech/nixos-router/backend/internal/wifi"
)

func (s *Server) handleWiFiStations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiHTTPError(w, "method not allowed", 405)
		return
	}
	if s.wifiInterface == "" {
		writeJSON(w, 200, map[string]any{"available": false, "reason": "No managed WiFi interface configured"})
		return
	}
	var stations []wifi.Station
	var err error
	if observer, ok := s.managedRuntime.(interface {
		Stations(context.Context) ([]wifi.Station, error)
	}); ok {
		stations, err = observer.Stations(r.Context())
	} else if s.managedRuntime == nil { // preview observations are still real if the local radio is accessible
		var raw string
		raw, err = s.runner.Output("iw", "dev", s.wifiInterface, "station", "dump")
		if err == nil {
			stations, err = wifi.ParseStations(raw)
		}
	} else {
		writeJSON(w, 200, map[string]any{"available": false, "reason": "WiFi station observer unavailable"})
		return
	}
	if err != nil {
		writeJSON(w, 200, map[string]any{"available": false, "reason": "Cannot query associated stations"})
		return
	}
	leases, _, _ := s.leaseSnapshot(r.Context())
	for i := range stations {
		for _, lease := range leases {
			if strings.EqualFold(stations[i].MAC, lease.MAC) && (lease.Expires.IsZero() || lease.Expires.After(time.Now())) {
				stations[i].IP = lease.IP
				if lease.Hostname != "*" {
					stations[i].Hostname = lease.Hostname
				}
				break
			}
		}
	}
	writeJSON(w, 200, map[string]any{"available": true, "source": "iw", "interface": s.wifiInterface, "stations": stations})
}

func (s *Server) handleWiFiChannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiHTTPError(w, "method not allowed", 405)
		return
	}
	observer, ok := s.managedRuntime.(interface {
		WiFiChannel(context.Context) (managed.WiFiChannelStatus, error)
	})
	if !ok {
		writeJSON(w, 200, map[string]any{"available": false, "reason": "Managed WiFi runtime unavailable"})
		return
	}
	status, err := observer.WiFiChannel(r.Context())
	if err != nil {
		writeJSON(w, 200, map[string]any{"available": false, "reason": "Cannot query WiFi channel"})
		return
	}
	writeJSON(w, 200, map[string]any{"available": true, "mode": status.Mode, "channel": status.Channel, "reason": status.Reason})
}

func (s *Server) handleWiFiReevaluate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiHTTPError(w, "method not allowed", 405)
		return
	}
	if !s.applyMu.TryLock() {
		writeJSON(w, 409, map[string]string{"error": "apply_busy"})
		return
	}
	defer s.applyMu.Unlock()
	observer, ok := s.managedRuntime.(interface {
		ReevaluateWiFi(context.Context) (managed.WiFiChannelStatus, error)
	})
	if !ok {
		apiHTTPError(w, "managed WiFi unavailable", 503)
		return
	}
	status, err := observer.ReevaluateWiFi(r.Context())
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": "wifi_evaluation_failed", "message": "WiFi evaluation could not complete safely"})
		return
	}
	writeJSON(w, 200, status)
}
