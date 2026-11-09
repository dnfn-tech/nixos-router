package managed

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestDelegatedPrefixFormatsAndLifetimes(t *testing.T) {
	value := "2001:db8:1200::/56,1800,3600,class=0000000a,excluded=2001:db8:1200::/60 " +
		"fd12:3456::/64,0,4294967295,900,1800 " +
		"2001:db8:1300::/56,0,0"
	got, err := parseDelegatedPrefixes(value)
	if err != nil || len(got) != 3 {
		t.Fatalf("parse: %v %v", got, err)
	}
	if got[0].Preferred != 1800 || got[0].Valid != 3600 || got[0].Class != 10 {
		t.Fatalf("lost metadata: %+v", got[0])
	}
	if got[1].Preferred != 0 || got[1].Valid != ^uint32(0) || got[2].Valid != 0 {
		t.Fatalf("lost deprecation or withdrawal: %+v", got)
	}
	for i, want := range []string{"2001:db8:1200:10::/64", "fd12:3456::/64"} {
		p, err := got[i].lanPrefix()
		if err != nil || p.String() != want {
			t.Fatalf("LAN prefix %d: %s %v", i, p, err)
		}
	}
	if _, err := got[2].lanPrefix(); err == nil {
		t.Fatal("withdrawn prefix allocated to LAN")
	}
}

func TestDelegatedPrefixSelection(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"2001:db8:1:ff00::/56,1,2", "2001:db8:1:ff00::/64"},
		{"2001:db8:1::/60,1,2,excluded=2001:db8:1::/80", "2001:db8:1:1::/64"},
		{"2001:db8:1::/60,1,2,excluded=2001:db8:1:8::/61", "2001:db8:1::/64"},
		{"2001:db8:1::/64,1,2,excluded=2001:db8:1::/80", ""},
		{"2001:db8:1::/80,1,2", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			prefixes, err := parseDelegatedPrefixes(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			got, err := prefixes[0].lanPrefix()
			if tc.want == "" {
				if err == nil {
					t.Fatal("allocated unavailable /64")
				}
			} else if err != nil || got != netip.MustParsePrefix(tc.want) {
				t.Fatalf("got %v %v", got, err)
			}
		})
	}
}

func TestDelegatedPrefixRejectsMalformedInputAtomically(t *testing.T) {
	for _, value := range []string{
		"192.0.2.0/24,1,2", "::ffff:192.0.2.0/120,1,2", "fe80::/64,1,2", "ff00::/8,1,2", "::/0,1,2",
		"2001:db8::1/56,1,2", "2001:db8::/129,1,2", "2001:db8::/56,3,2",
		"2001:db8::/56,-1,2", "2001:db8::/56,1,4294967296", "2001:db8::/56,1,2,3",
		"2001:db8::/56,1,2,0,bad", "2001:db8::/56,1,2,class=secret-value",
		"2001:db8::/56,1,2,excluded=2001:db9::/64", "2001:db8::/56,1,2,excluded=2001:db8::/56",
		"2001:db8::/56,1,2,excluded=2001:db8::/64,excluded=2001:db8:0:1::/64",
		"2001:db8::/56,1,2,class=1,class=2", "2001:db8::/56,1,2,command=secret-value",
		"2001:db8::/56,1,2\nsecret-value", strings.Repeat(" ", 65537),
		strings.Repeat("2001:db8::/56,1,2 ", 129),
	} {
		got, err := parseDelegatedPrefixes("2001:db8:ffff::/64,1,2 " + value)
		if err == nil || got != nil {
			t.Fatalf("accepted malformed event: %v", err)
		}
		if strings.Contains(err.Error(), "secret-value") {
			t.Fatal("error echoes network input")
		}
	}
}

func TestDelegatedPrefixDuplicates(t *testing.T) {
	one, err := parseDelegatedPrefixes("2001:db8::/56,1,2")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := parseDelegatedPrefixes("2001:db8::/56,1,2 2001:0db8::/56,1,2")
	if err != nil || !reflect.DeepEqual(one, duplicate) {
		t.Fatalf("duplicate: %v %v", duplicate, err)
	}
	if got, err := parseDelegatedPrefixes("2001:db8::/56,1,2 2001:db8::/56,0,0"); err == nil || got != nil {
		t.Fatal("accepted conflicting duplicate")
	}
	if got, err := parseDelegatedPrefixes(""); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
