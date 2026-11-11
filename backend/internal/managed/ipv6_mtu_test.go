package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mtuNetworkFake struct {
	base          *ipv6NetworkFake
	current, link uint32
	present       bool
	failWrite     bool
	writes        int
}

func (r *mtuNetworkFake) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "ip" && strings.Join(args, " ") == "-j link show" {
		if !r.present {
			return "[]", nil
		}
		b, _ := json.Marshal([]map[string]any{{"ifname": "wan0", "mtu": r.link}})
		return string(b), nil
	}
	if name == "sysctl" {
		if len(args) != 2 {
			r.base.t.Fatal(args)
		}
		if args[0] == "-n" && args[1] == "net/ipv6/conf/wan0/mtu" {
			return strconv.FormatUint(uint64(r.current), 10), nil
		}
		prefix := "net/ipv6/conf/wan0/mtu="
		if args[0] != "-qw" || !strings.HasPrefix(args[1], prefix) {
			r.base.t.Fatal("unscoped MTU write", args)
		}
		b, err := os.ReadFile(r.base.journal)
		if err != nil {
			r.base.t.Fatal(err)
		}
		var j ipv6ResourceJournal
		if json.Unmarshal(b, &j) != nil || !j.Pending || j.MTU == nil || j.MTU.Original != 1500 {
			r.base.t.Fatal("MTU changed without write-ahead recovery", string(b))
		}
		if r.failWrite {
			r.failWrite = false
			return "", errors.New("injected MTU failure")
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(args[1], prefix), 10, 32)
		if err != nil {
			r.base.t.Fatal(err)
		}
		r.current = uint32(n)
		r.writes++
		return "", nil
	}
	return r.base.Run(ctx, name, args...)
}

func mtuFixture(t *testing.T) (*Manager, *mtuNetworkFake, string) {
	m, base, gen := resourceFixture(t)
	r := &mtuNetworkFake{base: base, current: 1500, link: 1500, present: true}
	m.opts.Runner = r
	return m, r, gen
}

func TestIPv6MTUJournalLifecycleAndRetry(t *testing.T) {
	m, r, gen := mtuFixture(t)
	event := leaseEvent(t, "bound")
	event.MTU = 1400
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", event, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if r.current != 1400 {
		t.Fatal("advertised IPv6 MTU not applied", r.current)
	}
	event.State, event.MTU = "updated", 1300
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", event, time.Unix(1001, 0)); err != nil {
		t.Fatal(err)
	}
	event.MTU = 1280
	r.failWrite = true
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", event, time.Unix(1002, 0)); err == nil {
		t.Fatal("MTU failure hidden")
	}
	j, err := m.readIPv6Journal()
	if err != nil || !j.Pending || j.MTU == nil {
		t.Fatal(j, err)
	}
	restarted, err := New(m.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.reconcileIPv6Event(context.Background(), gen, "wan0", event, time.Unix(1002, 0)); err != nil {
		t.Fatal("pending MTU could not recover", err)
	}
	if r.current != 1280 {
		t.Fatal(r.current)
	}
	if err = restarted.reconcileIPv6Event(context.Background(), gen, "wan0", ipv6Event{State: "stopped"}, time.Unix(1003, 0)); err != nil {
		t.Fatal(err)
	}
	j, err = restarted.readIPv6Journal()
	if err != nil || j.MTU != nil || j.Pending || r.current != 1500 || r.link != 1500 {
		t.Fatal("original MTU not restored", j, err, r.current)
	}
}

func TestIPv6MTUBoundsOwnershipAndVanishedLink(t *testing.T) {
	m, r, gen := mtuFixture(t)
	event := leaseEvent(t, "bound")
	event.MTU = 1600
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", event, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	if r.current != 1500 || r.writes != 0 {
		t.Fatal("MTU above link accepted")
	}
	event.State, event.MTU = "updated", 1400
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", event, time.Unix(1001, 0)); err != nil {
		t.Fatal(err)
	}
	r.current = 1450
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", ipv6Event{State: "stopped"}, time.Unix(1002, 0)); err == nil || r.current != 1450 {
		t.Fatal("external MTU overwritten", err, r.current)
	}
	r.current, r.link = 1300, 1300
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", ipv6Event{State: "stopped"}, time.Unix(1003, 0)); err != nil {
		t.Fatal("smaller link MTU not respected", err)
	}
	if r.current != 1300 {
		t.Fatal("restored beyond physical link")
	}
	r.current, r.link = 1500, 1500
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", event, time.Unix(1004, 0)); err != nil {
		t.Fatal(err)
	}
	r.present = false
	r.base.live = map[string]ipv6Resource{}
	if err := m.reconcileIPv6Event(context.Background(), gen, "wan0", ipv6Event{State: "stopped"}, time.Unix(1005, 0)); err != nil {
		t.Fatal("vanished WAN prevents cleanup", err)
	}
	j, err := m.readIPv6Journal()
	if err != nil || j.MTU != nil {
		t.Fatal(j, err)
	}
}
