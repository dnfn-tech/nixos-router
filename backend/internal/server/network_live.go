package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
	"github.com/dnfn-tech/nixos-router/backend/internal/managed"
)

var networkSampleClock = time.Now()

type liveInterface struct {
	Name     string `json:"name,omitempty"`
	Identity string `json:"identity,omitempty"`
	SampleMs int64  `json:"sampleMs"`
	Link     string `json:"link"`
	RXBytes  string `json:"rxBytes,omitempty"`
	TXBytes  string `json:"txBytes,omitempty"`
}

type physicalPort struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Link      string `json:"link"`
	SpeedMbps int    `json:"speedMbps,omitempty"`
	Duplex    string `json:"duplex,omitempty"`
}

func (s *Server) handleNetworkLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiHTTPError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	draft := s.configSnapshot()
	cfg, applied := s.networkRuntimeConfig(r.Context(), draft)
	if !applied {
		cfg = draft
	}
	wan, lan := liveInterface{Link: "unknown"}, liveInterface{Link: "unknown"}
	if applied {
		// The primary route in each family must agree with the managed WAN.
		// Never substitute the carrier Ethernet interface for a PPPoE session.
		eligible := func(name string) bool {
			if s.managedRuntime != nil && name != managed.WANEgress(cfg) {
				return false
			}
			if cfg.WAN.Mode != "pppoe" {
				return true
			}
			return s.ifaceValue(name, "type") == "512" &&
				(s.managedRuntime == nil || s.ifaceValue(name, "ifalias") == managed.PPPLinkAlias)
		}
		egress := s.defaultEgress(r.Context(), eligible)
		wan = s.liveInterface(egress, "")
		if egress == "" && s.linkState(cfg.WAN.Interface) == "down" {
			wan.Link = "down"
		}
		lan = s.liveInterface(cfg.LAN.BridgeName, "bridge")
	}
	ports := s.physicalPorts(cfg.WAN.Interface, cfg.LAN.Ports)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clock": strconv.FormatInt(networkSampleClock.UnixNano(), 10),
		"wan":   wan, "lan": lan, "ports": ports,
		"contractDownMbps": draft.WAN.ContractDownMbps,
		"contractUpMbps":   draft.WAN.ContractUpMbps,
	})
}

func (s *Server) networkRuntimeConfig(ctx context.Context, draft config.Config) (config.Config, bool) {
	if runtime, ok := s.managedRuntime.(interface {
		Applied(context.Context) (config.Config, bool, error)
	}); ok {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		cfg, known, err := runtime.Applied(ctx)
		return cfg, known && err == nil
	}
	if s.managedRuntime == nil {
		return draft, true
	}
	return s.runtimeConfig()
}

func (s *Server) defaultEgress(ctx context.Context, eligible func(string) bool) string {
	if s.runner.LookPath("ip") != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for _, family := range []string{"-4", "-6"} {
		out, err := s.commandOutput(ctx, "ip", "-j", family, "route", "show", "default")
		var routes []struct {
			Dev    string
			Flags  []string
			Metric uint32
		}
		if err != nil || json.Unmarshal([]byte(out), &routes) != nil {
			return ""
		}
		var best uint32
		var dev string
		found, ambiguous := false, false
		for _, route := range routes {
			if slices.Contains(route.Flags, "linkdown") {
				continue
			}
			if !found || route.Metric < best {
				best, dev, found, ambiguous = route.Metric, route.Dev, true, false
			} else if route.Metric == best && route.Dev != dev {
				ambiguous = true
			}
		}
		if found && !ambiguous && dev != "" && eligible(dev) {
			return dev
		}
	}
	return ""
}

func (s *Server) ifaceValue(name, key string) string {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return ""
	}
	value, _ := s.readFileTrim(filepath.Join(s.sysClassNet, name, key))
	return value
}

func (s *Server) ifaceHas(name, key string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	_, err := os.Stat(filepath.Join(s.sysClassNet, name, key))
	return err == nil
}

func (s *Server) linkState(name string) string {
	switch s.ifaceValue(name, "operstate") {
	case "down", "lowerlayerdown", "dormant", "notpresent":
		return "down"
	}
	switch s.ifaceValue(name, "carrier") {
	case "0":
		return "down"
	case "1":
		return "up"
	}
	if s.ifaceValue(name, "operstate") == "up" {
		return "up"
	}
	return "unknown"
}

func (s *Server) liveInterface(name, marker string) liveInterface {
	sample := liveInterface{Name: name, Link: "unknown"}
	if marker != "" && !s.ifaceHas(name, marker) {
		return sample
	}
	sample.Link = s.linkState(name)
	index := s.ifaceValue(name, "ifindex")
	if index == "" || sample.Link == "down" {
		return sample
	}
	changes := s.ifaceValue(name, "carrier_changes")
	for key, dst := range map[string]*string{"rx_bytes": &sample.RXBytes, "tx_bytes": &sample.TXBytes} {
		value := s.ifaceValue(name, "statistics/"+key)
		if n, err := strconv.ParseUint(value, 10, 64); err == nil {
			*dst = strconv.FormatUint(n, 10)
		}
	}
	if index != s.ifaceValue(name, "ifindex") || changes != s.ifaceValue(name, "carrier_changes") {
		return liveInterface{Name: name, Link: "unknown"}
	}
	sample.Identity = index + "/" + changes
	sample.SampleMs = time.Since(networkSampleClock).Milliseconds()
	return sample
}

func (s *Server) physicalPorts(wan string, lan []string) []physicalPort {
	ports := []physicalPort{}
	entries, _ := os.ReadDir(s.sysClassNet)
	for _, entry := range entries {
		name := entry.Name()
		if s.ifaceValue(name, "type") != "1" || !s.ifaceHas(name, "device") ||
			s.ifaceHas(name, "phy80211") || s.ifaceHas(name, "wireless") ||
			s.ifaceHas(name, "bridge") {
			continue
		}
		port := physicalPort{Name: name, Role: "other", Link: s.linkState(name)}
		for _, member := range lan {
			if member == name {
				port.Role = "lan"
			}
		}
		if name == wan {
			port.Role = "wan"
		}
		if port.Link == "up" {
			if speed, err := strconv.Atoi(s.ifaceValue(name, "speed")); err == nil && speed > 0 && speed < 4294967295 {
				port.SpeedMbps = speed
			}
			switch duplex := s.ifaceValue(name, "duplex"); duplex {
			case "full", "half":
				port.Duplex = duplex
			}
		}
		ports = append(ports, port)
	}
	return ports
}
