package managed

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type ipv6NetworkFake struct {
	t             *testing.T
	journal       string
	live          map[string]ipv6Resource
	foreign       map[string]bool
	mutations     int
	failAt        int
	afterMutation func()
}

func (r *ipv6NetworkFake) Run(ctx context.Context, name string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		r.t.Fatal("unbounded command")
	}
	if name == "sysctl" && len(args) == 2 && args[0] == "-n" {
		return "1500", nil
	}
	if name == "ip" && strings.Join(args, " ") == "-j link show" {
		return `[{"ifname":"wan0","mtu":1500},{"ifname":"br-lan","mtu":1500},{"ifname":"nr-ppp0","mtu":1500}]`, nil
	}
	if name != "ip" {
		r.t.Fatal("unexpected executable", name)
	}
	if strings.Contains(strings.Join(args, " "), "flush") {
		r.t.Fatal("broad flush")
	}
	if len(args) > 1 && args[0] == "-j" {
		var rows []map[string]any
		for key, item := range r.live {
			proto := "242"
			if item.Kind == "address" {
				// iproute2 prints unknown address protocols in hexadecimal,
				// while route protocols use decimal strings.
				proto = "0xf2"
			}
			if r.foreign[key] {
				proto = "static"
			}
			if args[2] == "address" && item.Kind == "address" {
				rows = append(rows, map[string]any{"ifname": item.Interface, "addr_info": []any{map[string]any{"family": "inet6", "local": item.Prefix.Addr().String(), "prefixlen": item.Prefix.Bits(), "protocol": proto}}})
			}
			if args[2] == "route" && item.Kind == "route" {
				row := map[string]any{"dst": item.Prefix.String(), "dev": item.Interface, "metric": item.Metric, "protocol": proto}
				if item.Gateway.IsValid() {
					row["gateway"] = item.Gateway.String()
				}
				rows = append(rows, row)
			}
		}
		if rows == nil {
			return "[]", nil
		}
		b, _ := json.Marshal(rows)
		return string(b), nil
	}
	r.mutations++
	b, err := os.ReadFile(r.journal)
	if err != nil {
		r.t.Fatal("mutation before journal", err)
	}
	var journal ipv6ResourceJournal
	if err = json.Unmarshal(b, &journal); err != nil || !journal.Pending {
		r.t.Fatal("mutation without pending journal", err)
	}
	if r.mutations == r.failAt {
		return "", errors.New("injected command failure")
	}
	get := func(key string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == key {
				return args[i+1]
			}
		}
		return ""
	}
	p, err := netip.ParsePrefix(args[3])
	if err != nil {
		r.t.Fatal(args, err)
	}
	item := ipv6Resource{Kind: args[1], Prefix: p, Interface: get("dev")}
	if item.Kind == "route" {
		n, _ := strconv.ParseUint(get("metric"), 10, 32)
		item.Metric = uint32(n)
		if get("via") != "" {
			item.Gateway = netip.MustParseAddr(get("via"))
		}
	}
	key := item.key()
	if item.Kind == "address" && args[2] == "replace" {
		for key, target := range map[string]*uint32{"preferred_lft": &item.Preferred, "valid_lft": &item.Valid} {
			if get(key) == "forever" {
				*target = ^uint32(0)
			} else {
				n, err := strconv.ParseUint(get(key), 10, 32)
				if err != nil {
					r.t.Fatal(err)
				}
				*target = uint32(n)
			}
		}
	}
	if args[2] == "del" {
		delete(r.live, key)
	} else {
		r.live[key] = item
	}
	if r.afterMutation != nil {
		r.afterMutation()
	}
	return "", nil
}

func TestIPv6ResourcesAgeWhileQueuedAndExecuting(t *testing.T) {
	m, r, gen := resourceFixture(t)
	now := time.Unix(1000, 0)
	m.opts.Clock = func() time.Time { return now }
	e, err := parseIPv6Event("bound", map[string]string{"ADDRESSES": "2001:db8::1/128,10,20 2001:db8::2/128,3,7 2001:db8::3/128,4294967295,4294967295"})
	if err != nil {
		t.Fatal(err)
	}
	r.afterMutation = func() { now = now.Add(5 * time.Second) }
	if err = m.reconcileIPv6Event(context.Background(), gen, "wan0", e, time.Unix(997, 0)); err != nil {
		t.Fatal(err)
	}
	if len(r.live) != 2 {
		t.Fatal("expired resource installed", r.live)
	}
	for _, item := range r.live {
		switch item.Prefix.Addr().String() {
		case "2001:db8::1":
			if item.Preferred != 7 || item.Valid != 17 {
				t.Fatal("queue delay extended lease", item)
			}
		case "2001:db8::3":
			if item.Valid != ^uint32(0) {
				t.Fatal("infinity changed", item)
			}
		default:
			t.Fatal("expired during previous command", item)
		}
	}
}

func TestIPv6ResourcesExpiredUpdateRemovesPreviousAssignment(t *testing.T) {
	m, r, gen := resourceFixture(t)
	now := time.Unix(1000, 0)
	m.opts.Clock = func() time.Time { return now }
	e := leaseEvent(t, "bound")
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", e, now); err != nil {
		t.Fatal(err)
	}
	// The callback is valid but waited longer than all finite leases. Old
	// kernel resources are deliberately left in the fake to verify deletion.
	now = now.Add(100 * time.Second)
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", e, time.Unix(1001, 0)); err != nil {
		t.Fatal(err)
	}
	if len(r.live) != 0 {
		t.Fatal("previous assignments retained after expiry", r.live)
	}
	j, err := m.readIPv6Journal()
	if err != nil || j.Pending || len(j.Resources) != 0 {
		t.Fatal("expired resources recorded as installed", j, err)
	}
}

func resourceFixture(t *testing.T) (*Manager, *ipv6NetworkFake, string) {
	m, gen := ipv6GenerationFixture(t)
	r := &ipv6NetworkFake{t: t, journal: filepath.Join(m.base(), "ipv6-resources.json"), live: map[string]ipv6Resource{}, foreign: map[string]bool{}}
	m.opts.Runner = r
	m.opts.Clock = func() time.Time { return time.Unix(1005, 0) }
	return m, r, gen
}

func TestIPv6ResourcesUnusableDelegationWithdrawsOldLAN(t *testing.T) {
	for _, prefixes := range []string{
		"2001:db8:200::/80,30,60",
		"2001:db8:200::/64,30,60,excluded=2001:db8:200::/80",
		"2001:db8:200::/80,30,60 2001:db8:300::/56,30,60",
	} {
		t.Run(prefixes, func(t *testing.T) {
			m, r, gen := resourceFixture(t)
			now := time.Unix(1000, 0)
			if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", leaseEvent(t, "bound"), now); err != nil {
				t.Fatal(err)
			}
			e := leaseEvent(t, "updated")
			var err error
			e.Prefixes, err = parseDelegatedPrefixes(prefixes)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.reconcileIPv6Event(context.Background(), gen, "wan0", e, now.Add(time.Second)); err != nil {
				t.Fatal("valid upstream update must withdraw stale LAN resources", err)
			}
			wan, lan := 0, 0
			for _, item := range r.live {
				if item.Interface == "wan0" {
					wan++
					continue
				}
				lan++
				if !netip.MustParsePrefix("2001:db8:300::/64").Contains(item.Prefix.Addr()) {
					t.Fatal("stale or excluded LAN resource survived", item)
				}
			}
			wantLAN := 0
			if strings.Contains(prefixes, "2001:db8:300") {
				wantLAN = 2
			}
			if wan != 3 || lan != wantLAN {
				t.Fatalf("WAN=%d LAN=%d, want 3 and %d", wan, lan, wantLAN)
			}
		})
	}
}

func TestIPv6ResourcesRecoverInterruptedReconcile(t *testing.T) {
	m, r, gen := resourceFixture(t)
	r.failAt = 2
	e := leaseEvent(t, "bound")
	now := time.Unix(1000, 0)
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", e, now); err == nil {
		t.Fatal("failure claimed success")
	}
	b, _ := os.ReadFile(r.journal)
	var pending ipv6ResourceJournal
	if err := json.Unmarshal(b, &pending); err != nil || !pending.Pending || len(pending.Resources) == 0 {
		t.Fatal("lost recovery inventory", err)
	}
	r.failAt = 0
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", e, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(r.journal)
	var done ipv6ResourceJournal
	if err := json.Unmarshal(b, &done); err != nil || done.Pending || len(done.Resources) != len(r.live) {
		t.Fatal(done, err)
	}
	if len(r.live) < 4 {
		t.Fatal("WAN/LAN/route resources missing", r.live)
	}
	dns, err := os.ReadFile(filepath.Join(m.base(), "wan6-resolv.conf"))
	if err != nil || !strings.Contains(string(dns), "nameserver fe80::53%wan0") {
		t.Fatal(string(dns), err)
	}
	unbound := leaseEvent(t, "unbound")
	if err = m.reconcileIPv6Event(context.Background(), gen, "wan0", unbound, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, item := range r.live {
		if item.Interface != "wan0" || item.Prefix.Addr() == netip.MustParseAddr("2001:db8::2") {
			t.Fatal("DHCP or LAN resources survived unbound", item)
		}
	}
	dns, _ = os.ReadFile(filepath.Join(m.base(), "wan6-resolv.conf"))
	if strings.Contains(string(dns), "2001:db8::53") {
		t.Fatal("stale DHCP DNS", string(dns))
	}
}

func TestIPv6ResourcesRefuseForeignCollisionAndCorruptJournal(t *testing.T) {
	for _, mode := range []string{"foreign", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			m, r, gen := resourceFixture(t)
			if mode == "foreign" {
				item := ipv6Resource{Kind: "address", Interface: "wan0", Prefix: netip.MustParsePrefix("2001:db8::2/64")}
				r.live[item.key()] = item
				r.foreign[item.key()] = true
			} else {
				if err := os.WriteFile(r.journal, []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", leaseEvent(t, "bound"), time.Unix(1000, 0)); err == nil {
				t.Fatal("unsafe reconcile accepted")
			}
			if r.mutations != 0 {
				t.Fatal("mutated before preflight")
			}
		})
	}
}

func TestIPv6ResourcesSwitchCleansBeforeNewGeneration(t *testing.T) {
	m, r, gen := resourceFixture(t)
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", leaseEvent(t, "bound"), time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(m.base(), "generation-next")
	if err := os.Mkdir(next, 0700); err != nil {
		t.Fatal(err)
	}
	r.failAt = r.mutations + 1
	if err := m.switchFiles(context.Background(), next); err == nil {
		t.Fatal("switch ignored cleanup failure")
	}
	current, _ := os.Readlink(filepath.Join(m.base(), "current"))
	if current != gen {
		t.Fatal("switched despite failure")
	}
	r.failAt = 0
	if err := m.switchFiles(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(r.live) != 0 {
		t.Fatal("resources survived switch", r.live)
	}
	dns, _ := os.ReadFile(filepath.Join(m.base(), "wan6-resolv.conf"))
	if len(dns) != 0 {
		t.Fatal("DNS survived switch")
	}
	count := r.mutations
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", leaseEvent(t, "bound"), time.Unix(1001, 0)); err != nil || r.mutations != count {
		t.Fatal("stale callback mutated network", err)
	}
}
