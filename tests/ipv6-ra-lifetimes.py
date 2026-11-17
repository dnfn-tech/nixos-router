"""Real RA lifetime regression in a disposable user/network namespace.

Usage: python tests/ipv6-ra-lifetimes.py /path/to/dnsmasq
No host network operations: the parent only launches unshare; the child checks
the namespace boundary before creating any link or changing namespace sysctls.
"""
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import tempfile
import time


def ip(*args):
    subprocess.run(["ip", *args], check=True, stdout=subprocess.DEVNULL)


def run_case(binary, case):
    withdraw = case != "deprecate-expire"
    ip("link", "add", "ra0", "type", "veth", "peer", "name", "client0")
    for iface, address in [("ra0", "fe80::1/64"), ("client0", "fe80::2/64")]:
        ip("link", "set", iface, "addrgenmode", "none")
        ip("link", "set", iface, "up")
        subprocess.run(["sysctl", "-qw", f"net.ipv6.conf.{iface}.accept_ra=0", f"net.ipv6.conf.{iface}.autoconf=0"], check=True)
        ip("-6", "address", "add", address, "dev", iface, "nodad")
    preferred, valid = (30, 60) if withdraw else (3, 8)
    ip("-6", "address", "add", "2001:db8:10::1/64", "dev", "ra0", "nodad", "preferred_lft", str(preferred), "valid_lft", str(valid))
    started = time.monotonic()
    rows = []
    dns_rows = []
    with tempfile.TemporaryDirectory(prefix="router-ra-test-") as state, socket.socket(socket.AF_INET6, socket.SOCK_RAW, socket.IPPROTO_ICMPV6) as sock:
        index = socket.if_nametoindex("client0")
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, b"client0\0")
        sock.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_MULTICAST_IF, index)
        sock.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_MULTICAST_HOPS, 255)
        sock.settimeout(0.1)
        with open(state + "/log", "w+") as log:
            proc = subprocess.Popen([binary, "--no-daemon", "--conf-file=/dev/null", "--user=root", "--port=0", "--interface=ra0", "--bind-dynamic", "--dhcp-range=::,constructor:ra0,ra-stateless,64", "--enable-ra", "--dhcp-option=option6:dns-server,[2001:db8:10::53]", "--ra-param=ra0,3,10", "--dhcp-leasefile=" + state + "/leases", "--pid-file=" + state + "/pid"], stdout=log, stderr=log)
            deleted, last_send = False, -1
            try:
                while time.monotonic() - started < (7 if withdraw else 10):
                    elapsed = time.monotonic() - started
                    if proc.poll() is not None and not (case == "shutdown" and deleted):
                        raise AssertionError("dnsmasq exited before RA verification")
                    if withdraw and elapsed >= 2 and not deleted:
                        if case == "shutdown":
                            proc.terminate()
                            proc.wait(timeout=3)
                        ip("-6", "address", "del", "2001:db8:10::1/64", "dev", "ra0")
                        actual = subprocess.check_output(["ip", "-j", "-6", "address", "show", "dev", "ra0"], text=True)
                        assert not any(a["scope"] == "global" for link in json.loads(actual) for a in link["addr_info"]), actual
                        deleted = True
                    # After learning the prefix, real clients need not solicit
                    # again. Withdrawal must arrive as an unsolicited RA.
                    if elapsed - last_send >= 0.5 and (not withdraw or elapsed < 2):
                        sock.sendto(bytes([133, 0, 0, 0, 0, 0, 0, 0]), ("ff02::2", 0, 0, index))
                        last_send = elapsed
                    try:
                        data, _ = sock.recvfrom(4096)
                    except socket.timeout:
                        continue
                    if len(data) < 16 or data[0] != 134:
                        continue
                    position = 16
                    while position + 2 <= len(data) and data[position + 1]:
                        length = data[position + 1] * 8
                        if position + length > len(data):
                            raise AssertionError("truncated RA option")
                        if data[position] == 3 and length == 32 and data[position + 2] == 64:
                            prefix = socket.inet_ntop(socket.AF_INET6, data[position + 16:position + 32])
                            if prefix == "2001:db8:10::":
                                life, pref = struct.unpack_from("!II", data, position + 4)
                                router_life = struct.unpack_from("!H", data, 6)[0]
                                rows.append((round(time.monotonic() - started, 3), life, pref, router_life))
                        if data[position] == 25 and length >= 24:
                            dns_life = struct.unpack_from("!I", data, position + 4)[0]
                            assert dns_life <= struct.unpack_from("!H", data, 6)[0], ("DNS outlives router advertisement", dns_life)
                            if struct.unpack_from("!H", data, 6)[0] == 0:
                                assert dns_life == 0, ("DNS retained after router withdrawal", dns_life)
                            dns_rows.append((round(time.monotonic() - started, 3), dns_life))
                        position += length
                assert any(t < 2 and v > 0 and p > 0 for t, v, p, _ in rows), rows
                if withdraw:
                    zero = [t for t, v, p, router in rows if t >= 2 and v == p == router == 0]
                    assert zero and min(zero) < 5, ("missing prompt zero-lifetime withdrawal", rows)
                    assert all(v == p == 0 for t, v, p, _ in rows if t >= min(zero)), rows
                else:
                    assert any(3 <= t < 7 and v > 0 and p == 0 for t, v, p, _ in rows), ("deprecation removed valid prefix", rows)
                    assert all(v == 0 or t + v <= valid + 1.5 for t, v, _, _ in rows), ("lease deadline extended", rows)
                    assert any(t >= 7 and v == p == 0 for t, v, p, _ in rows), ("natural expiration not advertised", rows)
                if case == "shutdown":
                    assert any(t < 2 and life > 0 for t, life in dns_rows), ("no initial DNS advertisement", dns_rows)
                    assert any(t >= 2 and life == 0 for t, life in dns_rows), ("DNS not withdrawn on shutdown", dns_rows)
                print(json.dumps({"case": case, "packets": rows, "dns": dns_rows}), flush=True)
            except BaseException:
                log.flush()
                log.seek(0)
                print(log.read(), file=sys.stderr)
                raise
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
    ip("link", "delete", "ra0")


def run_renumber_case(binary):
    """Keep the surviving prefix usable through deprecation, deletion and reuse."""
    ip("link", "add", "ra0", "type", "veth", "peer", "name", "client0")
    for iface, address in [("ra0", "fe80::1/64"), ("client0", "fe80::2/64")]:
        ip("link", "set", iface, "addrgenmode", "none")
        subprocess.run(["sysctl", "-qw", f"net/ipv6/conf/{iface}/accept_ra=0", f"net/ipv6/conf/{iface}/autoconf=0"], check=True)
        ip("link", "set", iface, "up")
        ip("-6", "address", "add", address, "dev", iface, "nodad")
    old, new = "2001:db8:10::1/64", "2001:db8:20::1/64"
    ip("-6", "address", "add", old, "dev", "ra0", "nodad", "preferred_lft", "30", "valid_lft", "60")
    rows = []
    with tempfile.TemporaryDirectory(prefix="router-ra-renumber-") as state, socket.socket(socket.AF_INET6, socket.SOCK_RAW, socket.IPPROTO_ICMPV6) as sock:
        index = socket.if_nametoindex("client0")
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, b"client0\0")
        sock.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_MULTICAST_IF, index)
        sock.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_MULTICAST_HOPS, 255)
        sock.settimeout(0.1)
        with open(state + "/log", "w+") as log:
            proc = subprocess.Popen([binary, "--no-daemon", "--conf-file=/dev/null", "--user=root", "--port=0", "--interface=ra0", "--bind-dynamic", "--dhcp-range=::,constructor:ra0,ra-stateless,64", "--enable-ra", "--dhcp-option=option6:dns-server,[fe80::1]", "--ra-param=ra0,3,10", "--dhcp-leasefile=" + state + "/leases", "--pid-file=" + state + "/pid"], stdout=log, stderr=log)
            started, last_send, stage = time.monotonic(), -1, 0
            try:
                while time.monotonic() - started < 9:
                    elapsed = time.monotonic() - started
                    assert proc.poll() is None, "dnsmasq exited during renumbering"
                    if stage == 0 and elapsed >= 2:
                        ip("-6", "address", "add", new, "dev", "ra0", "nodad", "preferred_lft", "30", "valid_lft", "60")
                        ip("-6", "address", "change", old, "dev", "ra0", "preferred_lft", "0", "valid_lft", "20")
                        stage = 1
                    if stage == 1 and elapsed >= 4:
                        ip("-6", "address", "del", old, "dev", "ra0")
                        stage = 2
                    if stage == 2 and elapsed >= 6:
                        ip("-6", "address", "add", old, "dev", "ra0", "nodad", "preferred_lft", "30", "valid_lft", "60")
                        stage = 3
                    if elapsed - last_send >= 0.4:
                        sock.sendto(bytes([133, 0, 0, 0, 0, 0, 0, 0]), ("ff02::2", 0, 0, index))
                        last_send = elapsed
                    try:
                        data, _ = sock.recvfrom(4096)
                    except socket.timeout:
                        continue
                    if len(data) < 16 or data[0] != 134:
                        continue
                    prefixes, dns, pos = {}, [], 16
                    while pos + 2 <= len(data) and data[pos + 1]:
                        length = data[pos + 1] * 8
                        assert pos + length <= len(data), "truncated RA"
                        if data[pos] == 3 and length == 32:
                            prefix = socket.inet_ntop(socket.AF_INET6, data[pos + 16:pos + 32])
                            assert prefix not in prefixes, "duplicate PIO in one RA"
                            prefixes[prefix] = struct.unpack_from("!II", data, pos + 4)
                        if data[pos] == 25:
                            dns.append(struct.unpack_from("!I", data, pos + 4)[0])
                        pos += length
                    rows.append((round(time.monotonic() - started, 3), struct.unpack_from("!H", data, 6)[0], prefixes, dns))
                a, b = "2001:db8:10::", "2001:db8:20::"
                assert any(2 <= t < 4 and a in p and b in p and p[a][0] > 0 and p[a][1] == 0 and p[b][1] > 0 for t, _, p, _ in rows), ("deprecation damaged prefix coexistence", rows)
                assert any(4 <= t < 6 and p.get(a) == (0, 0) and p.get(b, (0, 0))[1] > 0 and router > 0 and dns and min(dns) > 0 for t, router, p, dns in rows), ("withdrawal damaged surviving router/DNS", rows)
                assert any(t >= 6 and p.get(a, (0, 0))[1] > 0 and p.get(b, (0, 0))[1] > 0 for t, _, p, _ in rows), ("withdrawn prefix cannot recover", rows)
                for t, router, p, dns in rows:
                    if b in p:
                        assert p[b][0] + t <= 63.5, ("surviving lease extended", rows)
                        assert router > 0 and dns and 0 < min(dns) <= max(dns) <= router, ("surviving router/DNS lost", rows)
                print(json.dumps({"case": "renumber-recover", "packets": rows}), flush=True)
            except BaseException:
                log.flush()
                log.seek(0)
                print(log.read(), file=sys.stderr)
                raise
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
    ip("link", "delete", "ra0")


if __name__ == "__main__":
    binary = str(Path(sys.argv[1]).resolve(strict=True))
    current = os.readlink("/proc/self/ns/net")
    if len(sys.argv) == 2:
        subprocess.run(["unshare", "--user", "--map-root-user", "--net", sys.executable, __file__, binary, current], check=True, timeout=55)
    else:
        assert current != sys.argv[2], "refusing network operations in the parent namespace"
        run_case(binary, "shutdown")
        run_case(binary, "withdraw")
        run_case(binary, "deprecate-expire")
        run_renumber_case(binary)
