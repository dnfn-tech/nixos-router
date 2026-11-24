package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The host service uses this marker to skip scheduled updates.
// Its absence preserves the existing enabled state on upgrade.
const (
	ddnsDisabledFile = "cloudflare-ddns.disabled"
	ddnsSettingsFile = "ddns-settings.json"
)

var (
	ddnsLabel  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	ddnsZoneID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	ddnsToken  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)
)

type ddnsSettings struct {
	Record   string `json:"record"`
	ZoneID   string `json:"zoneId,omitempty"`
	APIToken string `json:"apiToken,omitempty"`
}

// ManagedDDNSConfig is public metadata supplied by the host updater's configuration.
type ManagedDDNSConfig struct {
	Provider         string `json:"provider,omitempty"`
	Record           string `json:"record"`
	RecordType       string `json:"recordType,omitempty"`
	Interface        string `json:"interface,omitempty"`
	SettingsEditable bool   `json:"settingsEditable,omitempty"`
	IntervalMinutes  int    `json:"intervalMinutes,omitempty"`
}

func (s *Server) managedDDNSConfigured() bool {
	return s.managedRuntime != nil && (s.managedDDNSConfig.Record != "" || s.managedDDNSConfig.SettingsEditable) && s.stateDir != ""
}

func (s *Server) handleManagedDDNS(w http.ResponseWriter, r *http.Request) {
	if !s.managedDDNSConfigured() {
		apiHTTPError(w, "host-managed DDNS is unavailable", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodPut, http.MethodPatch:
	default:
		w.Header().Set("Allow", "GET, PUT, PATCH")
		apiHTTPError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.ddnsMu.Lock()
	defer s.ddnsMu.Unlock()

	path := filepath.Join(s.stateDir, ddnsDisabledFile)
	settingsPath := filepath.Join(s.stateDir, ddnsSettingsFile)
	var settings ddnsSettings
	if s.managedDDNSConfig.SettingsEditable {
		data, err := os.ReadFile(settingsPath)
		if (err != nil && !errors.Is(err, os.ErrNotExist)) || (err == nil && json.Unmarshal(data, &settings) != nil) {
			apiHTTPError(w, "could not read DDNS settings", http.StatusInternalServerError)
			return
		}
	}
	switch r.Method {
	case http.MethodPatch:
		if !s.managedDDNSConfig.SettingsEditable {
			apiHTTPError(w, "DDNS settings are managed by the host", http.StatusMethodNotAllowed)
			return
		}
		var input ddnsSettings
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || dec.Decode(new(any)) != io.EOF {
			apiHTTPError(w, "invalid DDNS settings", http.StatusBadRequest)
			return
		}
		input.Record = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input.Record), "."))
		if !validDDNSRecord(input.Record) {
			apiHTTPError(w, "invalid DDNS domain", http.StatusBadRequest)
			return
		}
		if input.APIToken != "" {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			if r.TLS == nil && !net.ParseIP(host).IsLoopback() {
				apiHTTPError(w, "use HTTPS or a local SSH tunnel to save the API token", http.StatusForbidden)
				return
			}
			if !ddnsZoneID.MatchString(input.ZoneID) || !ddnsToken.MatchString(input.APIToken) {
				apiHTTPError(w, "a valid Zone ID and API token are required together", http.StatusBadRequest)
				return
			}
		} else {
			if input.ZoneID != "" && input.ZoneID != settings.ZoneID {
				apiHTTPError(w, "changing the Zone ID requires a new API token", http.StatusBadRequest)
				return
			}
			input.ZoneID, input.APIToken = settings.ZoneID, settings.APIToken
		}
		if err := saveDDNSSettings(settingsPath, input); err != nil {
			log.Printf("persist managed DDNS settings: %v", err)
			apiHTTPError(w, "could not save DDNS settings", http.StatusInternalServerError)
			return
		}
		settings = input
		if s.db != nil {
			user, _, _ := s.getSession(r)
			s.db.AddAudit(user, "ddns_update", "settings updated")
		}
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
	}

	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("read managed DDNS state: %v", err)
		apiHTTPError(w, "could not read DDNS state", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	public := s.managedDDNSConfig
	if settings.Record != "" {
		public.Record = settings.Record
	}
	writeJSON(w, http.StatusOK, struct {
		ManagedDDNSConfig
		Enabled         bool   `json:"enabled"`
		ZoneID          string `json:"zoneId,omitempty"`
		TokenConfigured bool   `json:"tokenConfigured"`
	}{public, errors.Is(err, os.ErrNotExist), settings.ZoneID, settings.APIToken != ""})
}

func validDDNSRecord(record string) bool {
	labels := strings.Split(record, ".")
	if len(record) > 253 || len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !ddnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func saveDDNSSettings(path string, settings ddnsSettings) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".ddns-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := json.NewEncoder(file).Encode(settings); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
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
