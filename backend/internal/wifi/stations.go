package wifi

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

type Station struct {
	MAC              string `json:"mac"`
	SignalDBm        int    `json:"signalDBm"`
	ConnectedSeconds int    `json:"connectedSeconds"`
	TXBytes          uint64 `json:"txBytes"`
	RXBytes          uint64 `json:"rxBytes"`
	IP               string `json:"ip,omitempty"`
	Hostname         string `json:"hostname,omitempty"`
}

func ParseStations(raw string) ([]Station, error) {
	if len(raw) > 1<<20 || strings.ContainsRune(raw, 0) {
		return nil, errors.New("invalid station observation")
	}
	stations := []Station{}
	var current *Station
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Station ") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return nil, errors.New("invalid station identity")
			}
			mac, e := net.ParseMAC(fields[1])
			if e != nil || len(mac) != 6 {
				return nil, errors.New("invalid station identity")
			}
			stations = append(stations, Station{MAC: strings.ToLower(mac.String())})
			current = &stations[len(stations)-1]
			continue
		}
		if current == nil {
			return nil, errors.New("unexpected station output")
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "signal:":
			v, e := strconv.Atoi(fields[1])
			if e == nil {
				current.SignalDBm = v
			}
		case "connected":
			if len(fields) > 2 && fields[1] == "time:" {
				v, _ := strconv.Atoi(fields[2])
				current.ConnectedSeconds = v
			}
		case "tx":
			if len(fields) > 2 && fields[1] == "bytes:" {
				v, _ := strconv.ParseUint(fields[2], 10, 64)
				current.TXBytes = v
			}
		case "rx":
			if len(fields) > 2 && fields[1] == "bytes:" {
				v, _ := strconv.ParseUint(fields[2], 10, 64)
				current.RXBytes = v
			}
		}
	}
	return stations, nil
}
