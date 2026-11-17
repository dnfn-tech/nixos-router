package managed

import (
	"net/netip"
	"testing"
)

func TestIPv6DualLANPrefixAllocation(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, excluded string
		want                   []string
	}{
		{"normal /60", "2001:db8:100::/60", "", []string{"2001:db8:100::/64", "2001:db8:100:1::/64"}},
		{"exclude first pair", "2001:db8:100::/60", "2001:db8:100::/63", []string{"2001:db8:100:2::/64", "2001:db8:100:3::/64"}},
		{"exclude sub64 of second", "2001:db8:100::/60", "2001:db8:100:1:8000::/65", []string{"2001:db8:100::/64", "2001:db8:100:2::/64"}},
		{"exact pair", "2001:db8:100::/63", "", []string{"2001:db8:100::/64", "2001:db8:100:1::/64"}},
		{"only one /64", "2001:db8:100::/64", "", nil},
		{"pair excluded half", "2001:db8:100::/63", "2001:db8:100:1::/64", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := ipv6Delegation{Prefix: netip.MustParsePrefix(tc.prefix), Valid: 3600, Preferred: 1800}
			if tc.excluded != "" {
				d.Excluded = netip.MustParsePrefix(tc.excluded)
			}
			got, err := d.lanPrefixes(2)
			if tc.want == nil {
				if err == nil || len(got) != 0 {
					t.Fatal("partial/overlapping dual delegation accepted", got, err)
				}
				return
			}
			if err != nil || len(got) != len(tc.want) {
				t.Fatal(got, err)
			}
			for i, p := range got {
				if p.String() != tc.want[i] || p.Bits() != 64 || !d.Prefix.Contains(p.Addr()) || (d.Excluded.IsValid() && p.Overlaps(d.Excluded)) {
					t.Fatal(got)
				}
			}
			if got[0].Overlaps(got[1]) {
				t.Fatal("two networks share a prefix", got)
			}
		})
	}
}
