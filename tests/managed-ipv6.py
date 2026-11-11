import ipaddress
import json
import shlex
import time

router.start(allow_reboot=True)
start_all()
upstream.wait_for_unit("kea-dhcp6-server.service")
upstream.wait_until_succeeds("ss -H -6 -lun sport = :547 | grep -q .", timeout=30)
upstream.wait_for_unit("radvd.service")
upstream.wait_for_unit("network-addresses-lo.service")
upstream.succeed("ip -6 -o address show lo | grep '2001:db8:ffff::1/128'")
router.wait_for_unit("nixos-router-backend.service", timeout=180)
client.wait_until_succeeds("ip -4 -o addr show eth1 | grep 'inet 192.168.50.'", timeout=120)
base = "http://192.168.50.1:8080/api/v1"

def api(method, path, body=None):
    command = "curl --connect-timeout 3 --max-time 20 -fsS -b /tmp/cookie -c /tmp/cookie -X " + method
    if method == "PUT":
        revision = api("GET", "/config")["config"]["revision"]
        command += " -H " + shlex.quote("If-Match: " + json.dumps(revision))
    if body is not None:
        command += " -H 'Content-Type: application/json' --data " + shlex.quote(json.dumps(body))
    return json.loads(client.succeed(command + " " + shlex.quote(base + path)))

def apply(config):
    api("PUT", "/config", config)
    job = api("POST", "/apply")
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        time.sleep(1)
        rc, raw = client.execute("curl --connect-timeout 2 --max-time 5 -fsS -b /tmp/cookie " + shlex.quote(base + "/jobs/" + job["jobId"]))
        if rc in (7, 28, 52, 56):
            continue
        assert rc == 0, (rc, raw)
        result = json.loads(raw)
        if result["status"] in ("success", "failed"):
            assert result["status"] == "success", result
            return
    raise AssertionError("IPv6 configuration transaction timed out")

api("POST", "/session", {"username": "admin", "password": "test-password-123"})
cfg = api("GET", "/config")["config"]
cfg["ipv6"] = {"enable": True, "wanMode": "dhcpv6", "lanPrefixDelegation": True, "lanRouterAdvertisement": True}
cfg["dns"]["upstreams"] = []
# The ISP routes its delegation aggregate to the CPE's observed link-local
# next-hop. This configures only the test ISP, not the router under test.
addresses = json.loads(router.succeed("ip -j -6 address show eth1"))[0]["addr_info"]
next_hop = next(a["local"] for a in addresses if a["scope"] == "link")
upstream.succeed("ip -6 route replace 2001:db8:100::/56 via " + shlex.quote(next_hop) + " dev eth1")

with subtest("API apply acquires real IA_NA and PD, then routes client traffic"):
    apply(cfg)
    router.wait_for_unit("nixos-router-wan6.service")
    # A SLAAC address alone must not masquerade as successful IA_NA.
    check_ia = "import ipaddress,json,subprocess; rows=json.loads(subprocess.check_output(['ip','-j','-6','address','show','eth1'])); lo=ipaddress.ip_address('2001:db8:1::100'); hi=ipaddress.ip_address('2001:db8:1::200'); assert any(lo <= ipaddress.ip_address(a['local']) <= hi for r in rows for a in r['addr_info'] if a['scope']=='global')"
    try:
        router.wait_until_succeeds("python3 -c " + shlex.quote(check_ia), timeout=90)
    except Exception:
        print(router.succeed("journalctl -u nixos-router-wan6.service --no-pager -n 100"))
        print(router.succeed("ip -6 address show; ip -6 route show"))
        print(upstream.succeed("journalctl -u kea-dhcp6-server.service --no-pager -n 100"))
        raise
    router.wait_until_succeeds("ip -6 -o address show br-lan | grep '2001:db8:100:'", timeout=90)
    router.wait_until_succeeds("ip -6 route show default | grep 'proto 242'", timeout=90)
    client.wait_until_succeeds("ip -6 -o address show eth1 | grep '2001:db8:100:'", timeout=90)
    try:
        client.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:ffff::1", timeout=90)
    except Exception:
        for node in (client, router, upstream):
            print(node.name, node.succeed("ip -6 address show; ip -6 route show; ip -6 neigh show"))
        print(router.succeed("nft list ruleset"))
        print(router.succeed("sysctl net.ipv6.conf.all.forwarding net.ipv6.conf.eth1.force_forwarding net.ipv6.conf.br-lan.force_forwarding"))
        print(client.execute("ping -6 -c1 -W2 2001:db8:ffff::1"))
        raise
    router.wait_until_succeeds("test $(cat /proc/sys/net/ipv6/conf/eth1/mtu) = 1400", timeout=30)
    assert json.loads(router.succeed("ip -j link show eth1"))[0]["mtu"] == 1500
    client.wait_until_succeeds("ping -4 -c1 -W2 10.0.0.1", timeout=90)
    router.succeed("grep -Fx 'nameserver 2001:db8:1::1' /var/lib/nixos-router/managed/wan6-resolv.conf")
    router.succeed("grep -Fx 'nameserver 10.0.0.1' /var/lib/nixos-router/managed/wan4-resolv.conf")
    client.succeed("dig +short @192.168.50.1 v6.test.isp AAAA | grep '2001:db8:ffff::1'")
    client.succeed("dig -6 +short @2001:db8:100::1 v6.test.isp AAAA | grep '2001:db8:ffff::1'")
    # Observe the client's original IPv6 source at the remote endpoint.
    server = "import socket\nfrom http.server import BaseHTTPRequestHandler,HTTPServer\nclass H(BaseHTTPRequestHandler):\n def do_GET(self):\n  self.send_response(200); self.end_headers(); self.wfile.write(self.client_address[0].encode())\nclass S(HTTPServer): address_family=socket.AF_INET6\nS(('::',8090),H).serve_forever()\n"
    upstream.succeed("python3 -u -c " + shlex.quote(server) + " >/tmp/ipv6-http.log 2>&1 &")
    upstream.wait_for_open_port(8090)
    observed = client.succeed("curl --noproxy '*' -g -fsS --max-time 5 'http://[2001:db8:ffff::1]:8090/'").strip()
    assert ipaddress.ip_address(observed) in ipaddress.ip_network("2001:db8:100::/56"), observed
    wan = json.loads(router.succeed("ip -j -6 address show eth1"))[0]["addr_info"]
    wan_address = next(a["local"] for a in wan if a["scope"] == "global")
    upstream.fail("curl --noproxy '*' -g --connect-timeout 2 --max-time 3 " + shlex.quote("http://[" + wan_address + "]:8080/api/v1/health"))
    upstream.fail("nc -6 -z -w2 " + shlex.quote(wan_address) + " 22")

with subtest("RA MTU supports end-to-end large TCP transfers and IPv6 PTB"):
    server = "import socket\nfrom http.server import BaseHTTPRequestHandler,HTTPServer\nclass H(BaseHTTPRequestHandler):\n def do_POST(self):\n  data=self.rfile.read(int(self.headers['Content-Length'])); self.send_response(200); self.send_header('Content-Length',str(len(data))); self.end_headers(); self.wfile.write(data)\nclass S(HTTPServer): address_family=socket.AF_INET6\nS(('::',8091),H).serve_forever()\n"
    upstream.succeed("python3 -u -c " + shlex.quote(server) + " >/tmp/ipv6-large-http.log 2>&1 &")
    upstream.wait_for_open_port(8091)
    # 1452 bytes of ICMP payload produces a 1500-byte IPv6 packet, larger
    # than the learned WAN MTU. Require explicit PTB rather than any failure.
    ptb = client.succeed("ping -6 -M do -s 1452 -c1 -W2 2001:db8:ffff::1 2>&1 || true")
    assert "mtu=1400" in ptb or "mtu = 1400" in ptb, ptb
    code = "import http.client; data=bytes(range(256))*256; c=http.client.HTTPConnection('2001:db8:ffff::1',8091,timeout=15); c.request('POST','/',data); r=c.getresponse(); assert r.status==200; assert r.read()==data"
    client.succeed("python3 -c " + shlex.quote(code))
    # IPv4 retains its 1500-byte path despite the IPv6-only MTU setting.
    client.succeed("ping -4 -M do -s 1472 -c1 -W2 10.0.0.1")

with subtest("trusted host UDP policy reaches the actual IPv4 and IPv6 WAN"):
    router.wait_for_unit("test-host-udp.service")
    router.wait_until_succeeds("ss -H -6 -lun sport = :9994 | grep -q .", timeout=30)
    wan4 = json.loads(router.succeed("ip -j -4 address show eth1"))[0]["addr_info"][0]["local"]
    def udp_echo(node, address, port, family, allowed=True):
        code = "import socket; s=socket.socket(socket." + family + ",socket.SOCK_DGRAM); s.settimeout(2); s.connect((" + repr(address) + "," + str(port) + ")); s.send(b'host-service'); assert s.recv(1024)==b'host-service'"
        command = "python3 -c " + shlex.quote(code)
        (node.succeed if allowed else node.fail)(command)
    for family, address, lan_address in (("AF_INET", wan4, "192.168.50.1"), ("AF_INET6", wan_address, "2001:db8:100::1")):
        # Prove the blocked listener exists and serves LAN clients before
        # expecting a WAN timeout; an absent server cannot satisfy this test.
        udp_echo(client, lan_address, 9994, family)
        udp_echo(upstream, address, 9993, family)
        udp_echo(upstream, address, 9994, family, allowed=False)

with subtest("repeat apply and stale callback preserve current DNS and traffic"):
    old = router.succeed("readlink /var/lib/nixos-router/managed/current").strip()
    apply(cfg)
    client.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:ffff::1", timeout=90)
    before = router.succeed("cat /var/lib/nixos-router/managed/wan-resolv.conf")
    router.succeed("RDNSS=2001:db8:bad::53 router-executor ipv6-event --state-dir /var/lib/nixos-router --interfaces eth1,eth2,eth3 --generation " + shlex.quote(old) + " --interface eth1 --event bound")
    assert router.succeed("cat /var/lib/nixos-router/managed/wan-resolv.conf") == before

with subtest("DHCPv6 loss expires PD but retains RA and IPv4, then recovers"):
    # Stop the real server, leaving ISP router advertisements running. No
    # synthetic callback substitutes for the protocol client's lease timers.
    upstream.succeed("systemctl stop kea-dhcp6-server.service")
    router.wait_until_succeeds("test -z \"$(ip -6 -o address show br-lan scope global)\"", timeout=180)
    router.wait_until_succeeds("test ! -s /var/lib/nixos-router/managed/wan6-resolv.conf", timeout=30)
    router.succeed("ip -6 route show default | grep 'proto 242'")
    router.succeed("ip -6 -o address show eth1 | grep '2001:db8:1:0:'")
    router.succeed("grep -Fx 'nameserver 10.0.0.1' /var/lib/nixos-router/managed/wan-resolv.conf")
    client.succeed("ping -4 -c1 -W2 10.0.0.1")
    client.fail("ping -6 -c1 -W2 2001:db8:ffff::1")
    upstream.succeed("systemctl start kea-dhcp6-server.service")
    upstream.wait_until_succeeds("ss -H -6 -lun sport = :547 | grep -q .", timeout=30)
    router.wait_until_succeeds("python3 -c " + shlex.quote(check_ia), timeout=180)
    client.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:ffff::1", timeout=90)
    router.succeed("grep -Fx 'nameserver 2001:db8:1::1' /var/lib/nixos-router/managed/wan6-resolv.conf")

with subtest("router reboot recovers applied dual-stack configuration"):
    router.reboot()
    router.wait_for_unit("nixos-router-backend.service", timeout=180)
    router.wait_until_succeeds("python3 -c " + shlex.quote(check_ia), timeout=180)
    client.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:ffff::1", timeout=180)
    client.wait_until_succeeds("ping -4 -c1 -W2 10.0.0.1", timeout=90)
    assert api("GET", "/config")["config"]["ipv6"] == cfg["ipv6"]

with subtest("DHCPv6 without PD and SLAAC-only provide WAN IPv6 without LAN delegation"):
    cfg["ipv6"]["lanPrefixDelegation"] = False
    cfg["ipv6"]["lanRouterAdvertisement"] = False
    for mode in ("dhcpv6", "slaac"):
        cfg["ipv6"]["wanMode"] = mode
        apply(cfg)
        router.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:ffff::1", timeout=90)
        router.succeed("test -z \"$(ip -6 -o address show br-lan scope global)\"")
        router.succeed("test $(cat /proc/sys/net/ipv6/conf/br-lan/force_forwarding) = 0")
        router.wait_until_succeeds("test $(cat /proc/sys/net/ipv6/conf/eth1/mtu) = 1400", timeout=30)
        # Each Apply restarts WAN DHCP; IPv6 may be ready before IPv4.
        client.wait_until_succeeds("ping -4 -c1 -W2 10.0.0.1", timeout=90)
        if mode == "dhcpv6":
            router.wait_until_succeeds("python3 -c " + shlex.quote(check_ia), timeout=90)
        else:
            router.fail("python3 -c " + shlex.quote(check_ia))
    # Restore the routed configuration before testing full disable cleanup.
    cfg["ipv6"] = {"enable": True, "wanMode": "dhcpv6", "lanPrefixDelegation": True, "lanRouterAdvertisement": True}
    apply(cfg)
    client.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:ffff::1", timeout=90)

with subtest("disable withdraws owned IPv6 while IPv4 remains usable"):
    cfg["ipv6"]["enable"] = False
    apply(cfg)
    router.succeed("test -z \"$(ip -6 -o address show br-lan scope global)\"")
    router.succeed("test -z \"$(ip -6 route show proto 242)\"")
    router.succeed("test ! -s /var/lib/nixos-router/managed/wan6-resolv.conf")
    router.succeed("test $(cat /proc/sys/net/ipv6/conf/br-lan/force_forwarding) = 0")
    router.succeed("test $(cat /proc/sys/net/ipv6/conf/eth1/mtu) = 1500")
    client.fail("ping -6 -c1 -W2 2001:db8:ffff::1")
    client.wait_until_succeeds("ping -4 -c1 -W2 10.0.0.1", timeout=90)
