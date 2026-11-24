package server

import (
	"bytes"
	"context"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/dnfn-tech/nixos-router/backend/internal/config"
)

const (
	monitorInterval = 2 * time.Second
	portInterval    = 15 * time.Second
	maxSampleGap    = 3 * monitorInterval
)

type monitoringSnapshot struct {
	Clock            string         `json:"clock"`
	SampleMs         int64          `json:"sampleMs"`
	WAN              liveInterface  `json:"wan"`
	LAN              liveInterface  `json:"lan"`
	Ports            []physicalPort `json:"ports"`
	ContractDownMbps uint32         `json:"contractDownMbps"`
	ContractUpMbps   uint32         `json:"contractUpMbps"`
	CPUPercent       *float64       `json:"cpuPercent"`
	Memory           *memoryUsage   `json:"memory"`
}

type monitoringCache struct {
	mu       sync.Mutex
	inFlight *monitoringFlight
	revision string
	expires  time.Time
	value    monitoringSnapshot

	// Only the single collector accesses these fields.
	cpu     cpuSample
	ports   map[string]portObservation
	portDue time.Time
}

type monitoringFlight struct {
	done     chan struct{}
	revision string
	value    monitoringSnapshot
}

func (s *Server) monitoringSnapshot(ctx context.Context) (monitoringSnapshot, bool) {
	cache := &s.monitor
	for ctx.Err() == nil {
		cache.mu.Lock()
		draft := s.configSnapshot()
		if cache.inFlight == nil && cache.revision == draft.Revision && time.Now().Before(cache.expires) {
			value := cache.value
			cache.mu.Unlock()
			return value, true
		}
		if cache.inFlight == nil {
			cache.inFlight = &monitoringFlight{done: make(chan struct{}), revision: draft.Revision}
			go s.refreshMonitoring(cache.inFlight, draft, time.Now())
		}
		flight := cache.inFlight
		cache.mu.Unlock()
		select {
		case <-flight.done:
			if s.configSnapshot().Revision == flight.revision {
				return flight.value, true
			}
		case <-ctx.Done():
			return monitoringSnapshot{}, false
		}
	}
	return monitoringSnapshot{}, false
}

func (s *Server) refreshMonitoring(flight *monitoringFlight, draft config.Config, started time.Time) {
	cache := &s.monitor
	// A browser disconnect must not interrupt a refresh shared by other callers.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value := s.collectNetworkLive(ctx, draft)
	value.CPUPercent = cache.sampleCPU("/proc/stat")
	value.Memory = readMemoryUsage("/proc/meminfo")
	sampled := time.Now()
	value.Clock = strconv.FormatInt(networkSampleClock.UnixNano(), 10)
	value.SampleMs = sampled.Sub(networkSampleClock).Milliseconds()

	cache.mu.Lock()
	cache.value, cache.revision, cache.expires = value, draft.Revision, started.Add(monitorInterval)
	flight.value = value
	close(flight.done)
	cache.inFlight = nil
	cache.mu.Unlock()
}

type cpuSample struct {
	at    time.Time
	ticks [8]uint64
}

func (cache *monitoringCache) sampleCPU(path string) *float64 {
	data, err := os.ReadFile(path)
	line, _, _ := bytes.Cut(data, []byte("\n"))
	fields := bytes.Fields(line)
	previous := cache.cpu
	cache.cpu = cpuSample{}
	if err != nil || len(fields) < 9 || string(fields[0]) != "cpu" {
		return nil
	}
	next := cpuSample{at: time.Now()}
	for i, field := range fields[1:9] {
		n, err := strconv.ParseUint(string(field), 10, 64)
		if err != nil {
			return nil
		}
		next.ticks[i] = n
	}
	cache.cpu = next
	elapsed := next.at.Sub(previous.at)
	if previous.at.IsZero() || elapsed <= 0 || elapsed > maxSampleGap {
		return nil
	}
	var total, busy float64
	for i, value := range next.ticks {
		if value < previous.ticks[i] {
			return nil
		}
		delta := float64(value - previous.ticks[i])
		total += delta
		if i != 3 && i != 4 { // idle and iowait
			busy += delta
		}
	}
	if total == 0 {
		return nil
	}
	percent := 100 * busy / total
	return &percent
}

type memoryUsage struct {
	TotalBytes uint64 `json:"totalBytes"`
	UsedBytes  uint64 `json:"usedBytes"`
}

func readMemoryUsage(path string) *memoryUsage {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var total, available uint64
	var haveAvailable bool
	for _, line := range bytes.Split(data, []byte("\n")) {
		fields := bytes.Fields(line)
		if len(fields) == 0 {
			continue
		}
		key := string(fields[0])
		if key != "MemTotal:" && key != "MemAvailable:" {
			continue
		}
		if len(fields) != 3 || string(fields[2]) != "kB" {
			return nil
		}
		value, err := strconv.ParseUint(string(fields[1]), 10, 64)
		if err != nil || value > ^uint64(0)/1024 {
			return nil
		}
		if key == "MemTotal:" {
			total = value
		} else {
			available, haveAvailable = value, true
		}
	}
	if total == 0 || !haveAvailable || available > total {
		return nil
	}
	return &memoryUsage{TotalBytes: total * 1024, UsedBytes: (total - available) * 1024}
}

type portObservation struct {
	identity string
	speed    int
	duplex   string
}

func (s *Server) portObservations(wan string, lan []string) []physicalPort {
	cache := &s.monitor
	now := time.Now()
	refresh := !now.Before(cache.portDue)
	if refresh {
		cache.ports = make(map[string]portObservation)
		cache.portDue = now.Add(portInterval)
		entries, err := os.ReadDir(s.sysClassNet)
		if err != nil {
			cache.portDue = time.Time{}
		}
		for _, entry := range entries {
			name := entry.Name()
			if s.ifaceValue(name, "type") == "1" && s.ifaceHas(name, "device") &&
				!s.ifaceHas(name, "phy80211") && !s.ifaceHas(name, "wireless") &&
				!s.ifaceHas(name, "bridge") {
				cache.ports[name] = portObservation{}
			}
		}
	}
	return s.observeKnownPorts(wan, lan)
}

func (s *Server) portIdentity(name string) string {
	index := s.ifaceValue(name, "ifindex")
	if index == "" {
		return ""
	}
	return index + "/" + s.ifaceValue(name, "carrier_changes")
}

func (s *Server) observeKnownPorts(wan string, lan []string) []physicalPort {
	ports := make([]physicalPort, 0, len(s.monitor.ports))
	for _, name := range slices.Sorted(maps.Keys(s.monitor.ports)) {
		previous := s.monitor.ports[name]
		identity := s.portIdentity(name)
		if identity == "" || !s.ifaceHas(name, "device") {
			delete(s.monitor.ports, name)
			s.monitor.portDue = time.Time{}
			continue
		}
		port, next := s.observePort(name, wan, lan, identity, previous)
		s.monitor.ports[name] = next
		ports = append(ports, port)
	}
	return ports
}

func (s *Server) observePort(name, wan string, lan []string, identity string, previous portObservation) (physicalPort, portObservation) {
	port := physicalPort{Name: name, Role: "other", Link: s.linkState(name)}
	if slices.Contains(lan, name) {
		port.Role = "lan"
	}
	if name == wan {
		port.Role = "wan"
	}
	if port.Link != "up" {
		return port, portObservation{}
	}
	if previous.identity != identity {
		previous = portObservation{identity: identity}
		if speed, err := strconv.Atoi(s.ifaceValue(name, "speed")); err == nil && speed > 0 && uint64(speed) < 4294967295 {
			previous.speed = speed
		}
		switch duplex := s.ifaceValue(name, "duplex"); duplex {
		case "full", "half":
			previous.duplex = duplex
		}
	}
	if s.portIdentity(name) != identity {
		port.Link = "unknown"
		return port, portObservation{}
	}
	port.SpeedMbps, port.Duplex = previous.speed, previous.duplex
	return port, previous
}
