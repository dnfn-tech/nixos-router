package managed

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIPv6LANAdvertisementService(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		enabled, pd, ra, dns bool
	}{
		{"stateless", true, true, true, true},
		{"ra-only", true, true, true, false},
		{"disabled-draft", false, true, true, false},
		{"pd-without-ra", true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r, c := fixture(t)
			c.IPv6.Enable, c.IPv6.WANMode, c.IPv6.LANPD, c.IPv6.LANRA = tc.enabled, "dhcpv6", tc.pd, tc.ra
			c.LAN.DHCP.Enable = false
			c.DNS.EnableDNSMasq = tc.dns
			conf := m.render(c)["dnsmasq.conf"]
			advertise := tc.enabled && tc.pd && tc.ra
			if strings.Contains(conf, "enable-ra\n") != advertise {
				t.Fatalf("RA enable mismatch: %s", conf)
			}
			if advertise {
				mode := "ra-only"
				if tc.dns {
					mode = "ra-stateless"
				}
				if !strings.Contains(conf, "dhcp-range=::,constructor:"+c.LAN.BridgeName+","+mode+",64\n") {
					t.Fatalf("missing dynamic LAN prefix: %s", conf)
				}
				if !tc.dns && !strings.Contains(conf, "dhcp-option=option6:dns-server\n") {
					t.Fatalf("disabled DNS must not be advertised: %s", conf)
				}
			}
			dir, err := m.prepare(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if binary := os.Getenv("ROUTERD_TEST_DNSMASQ"); binary != "" {
				if out, err := exec.Command(binary, "--test", "--conf-file="+filepath.Join(dir, "dnsmasq.conf")).CombinedOutput(); err != nil {
					t.Fatalf("real dnsmasq rejected generated config: %v: %s", err, out)
				}
			}
			prepared := strings.Join(r.calls, "\n")
			if strings.Contains(prepared, "dnsmasq --test") != (advertise || tc.dns) {
				t.Fatal("missing syntax validation", prepared)
			}
			r.calls = nil
			// Public IPv6 capability remains closed pending complete forwarding VM.
			if err = m.activate(context.Background(), c, nil, dir); err != nil {
				t.Fatal(err)
			}
			calls := strings.Join(r.calls, "\n")
			if strings.Contains(calls, "systemctl restart nixos-router-dnsmasq.service") != (advertise || tc.dns) {
				t.Fatal("LAN service activation mismatch", calls)
			}
		})
	}
}
