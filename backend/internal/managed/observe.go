package managed

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Lease struct {
	Expires  time.Time `json:"expires"`
	MAC      string    `json:"mac"`
	IP       string    `json:"ip"`
	Hostname string    `json:"hostname"`
	ClientID string    `json:"clientId"`
}

// Leases exposes only the dedicated dnsmasq lease file, never a caller-chosen
// path. Missing or unreadable state is distinct from a successful empty read.
func (m *Manager) Leases(ctx context.Context) ([]Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(m.opts.StateDir, "dnsmasq.leases"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("lease snapshot is not a regular file")
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("lease snapshot exceeds size limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ParseLeases(data), nil
}

func ParseLeases(data []byte) []Lease {
	out := []Lease{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		var expires time.Time
		if secs, err := strconv.ParseInt(fields[0], 10, 64); err == nil && secs > 0 {
			expires = time.Unix(secs, 0)
		}
		clientID := ""
		if len(fields) >= 5 {
			clientID = fields[4]
		}
		out = append(out, Lease{Expires: expires, MAC: fields[1], IP: fields[2], Hostname: fields[3], ClientID: clientID})
	}
	return out
}
