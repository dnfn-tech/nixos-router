package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// The host service uses this marker to skip scheduled updates.
// Its absence preserves the existing enabled state on upgrade.
const ddnsDisabledFile = "cloudflare-ddns.disabled"

// ManagedDDNSConfig is public metadata supplied by the host updater's configuration.
type ManagedDDNSConfig struct {
	Provider        string `json:"provider,omitempty"`
	Record          string `json:"record"`
	RecordType      string `json:"recordType,omitempty"`
	Interface       string `json:"interface,omitempty"`
	IntervalMinutes int    `json:"intervalMinutes,omitempty"`
}

func (s *Server) managedDDNSConfigured() bool {
	return s.managedRuntime != nil && s.managedDDNSConfig.Record != "" && s.stateDir != ""
}

func (s *Server) handleManagedDDNS(w http.ResponseWriter, r *http.Request) {
	if !s.managedDDNSConfigured() {
		apiHTTPError(w, "host-managed DDNS is unavailable", http.StatusNotFound)
		return
	}

	path := filepath.Join(s.stateDir, ddnsDisabledFile)
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || input.Enabled == nil || dec.Decode(new(any)) != io.EOF {
			apiHTTPError(w, "expected an enabled boolean", http.StatusBadRequest)
			return
		}

		if err := setManagedDDNSEnabled(path, *input.Enabled); err != nil {
			log.Printf("persist managed DDNS state: %v", err)
			apiHTTPError(w, "could not save DDNS state", http.StatusInternalServerError)
			return
		}
		if s.db != nil {
			user, _, _ := s.getSession(r)
			s.db.AddAudit(user, "ddns_update", "enabled="+strconv.FormatBool(*input.Enabled))
		}
	default:
		w.Header().Set("Allow", "GET, PUT")
		apiHTTPError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("read managed DDNS state: %v", err)
		apiHTTPError(w, "could not read DDNS state", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		ManagedDDNSConfig
		Enabled bool `json:"enabled"`
	}{s.managedDDNSConfig, errors.Is(err, os.ErrNotExist)})
}

func setManagedDDNSEnabled(path string, enabled bool) error {
	if enabled {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}
