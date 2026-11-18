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
router.wait_for_unit("nixos-router-backend.service", timeout=180)
router.wait_for_unit("dual-radio-station.service")
client.wait_until_succeeds("ip -4 -o addr show eth1 | grep 'inet 192.168.50.'", timeout=120)
BASE = "http://192.168.50.1:8080/api/v1"
NS = "ip netns exec radio-station "
aggregate = ipaddress.ip_network("2001:db8:100::/56")


def api(method, path, body=None):
    command = "curl --connect-timeout 3 --max-time 20 -fsS -b /tmp/cookie -c /tmp/cookie -X " + method
    if method == "PUT":
        command += " -H " + shlex.quote("If-Match: " + json.dumps(api("GET", "/config")["config"]["revision"]))
    if body is not None:
        command += " -H 'Content-Type: application/json' --data " + shlex.quote(json.dumps(body))
    return json.loads(client.succeed(command + " " + shlex.quote(BASE + path)))


def apply(config):
    api("PUT", "/config", config)
    job = api("POST", "/apply")
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        time.sleep(1)
        rc, raw = client.execute("curl --connect-timeout 2 --max-time 5 -fsS -b /tmp/cookie " + shlex.quote(BASE + "/jobs/" + job["jobId"]))
        if rc in (7, 28, 52, 56):
            continue
        assert rc == 0, (rc, raw)
        result = json.loads(raw)
        if result["status"] in ("success", "failed"):
            assert result["status"] == "success", result
            payload = result["payload"]
            if isinstance(payload, str):
                payload = json.loads(payload)
            if payload.get("confirmation"):
                confirmation = api("POST", "/apply/confirm", {"id": payload["confirmation"]["id"]})
                assert confirmation["confirmation"]["state"] == "confirmed", confirmation
            return
    raise AssertionError("dual-stack configuration transaction timed out")


def global6(node, interface, prefix, namespace=""):
    rows = json.loads(node.succeed(namespace + "ip -j -6 address show dev " + interface))
    return next(a["local"] for r in rows for a in r["addr_info"] if a["scope"] == "global" and ipaddress.ip_address(a["local"]) in prefix and a.get("valid_life_time", 1) != 0)


def station4():
    router.succeed(NS + "udhcpc -n -q -i wlan1 -t 8 -T 2 -s /etc/dual-station-dhcp")
    assert router.succeed("cat /run/dual-station-ipv4").strip() in [f"192.168.60.{i}" for i in range(100,111)]
    router.succeed(NS + "ping -4 -c1 -W2 10.0.0.1")


def wait_dual():
    router.wait_until_succeeds(NS + "wpa_cli -p /run/dual-station-control -i wlan1 status | grep -Fx wpa_state=COMPLETED", timeout=120)
    router.succeed(NS + "wpa_cli -p /run/dual-station-control -i wlan1 status | grep -Fx key_mgmt=SAE")
    if RADIO_ADVANCED:
        router.succeed(NS + "wpa_cli -p /run/dual-station-control -i wlan1 status | grep -Fx wifi_generation=7")
        router.succeed("iw dev wlan0 info | grep -F 'width: 80 MHz'")
    for interface in ("br-lan", "wlan0"):
        code = "import ipaddress,json,subprocess; p=ipaddress.ip_network(" + repr(str(aggregate)) + "); r=json.loads(subprocess.check_output(['ip','-j','-6','address','show','dev'," + repr(interface) + "])); assert any(a['scope']=='global' and ipaddress.ip_address(a['local']) in p for row in r for a in row['addr_info'])"
        router.wait_until_succeeds("python3 -c " + shlex.quote(code), timeout=180)
    wired_gateway = global6(router,"br-lan",aggregate)
    wifi_gateway = global6(router,"wlan0",aggregate)
    wired_net = ipaddress.IPv6Network(wired_gateway + "/64", strict=False)
    wifi_net = ipaddress.IPv6Network(wifi_gateway + "/64", strict=False)
    assert not wired_net.overlaps(wifi_net), (wired_net,wifi_net)
    assert int(wifi_net.network_address) == int(wired_net.network_address) + (1 << 64), (wired_net,wifi_net)
    journal = json.loads(router.succeed("cat /var/lib/nixos-router/managed/ipv6-resources.json"))
    delegations = [ipaddress.IPv6Network(d["Prefix"]) for d in journal["Lease"]["Event"]["Prefixes"] if d["Valid"] > 0]
    assert any(wired_net.subnet_of(p) and wifi_net.subnet_of(p) for p in delegations), (delegations,wired_net,wifi_net)
    client.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:ffff::1", timeout=120)
    router.wait_until_succeeds(NS + "ping -6 -c1 -W2 2001:db8:ffff::1", timeout=120)
    router.succeed(NS + "dig -6 +short @" + wifi_gateway + " v6.test.isp AAAA | grep -Fx 2001:db8:ffff::1")
    client.succeed("dig -6 +short @" + wired_gateway + " v6.test.isp AAAA | grep -Fx 2001:db8:ffff::1")
    observed = router.succeed(NS + "curl --noproxy '*' -g -fsS --max-time 8 'http://[2001:db8:ffff::1]:8090/'").strip()
    assert ipaddress.ip_address(observed) in wifi_net, (observed,wifi_net)
    return wired_gateway,wifi_gateway,global6(client,"eth1",wired_net),global6(router,"wlan1",wifi_net,NS)


def isp_delegation(prefix, length, lease_name):
    global aggregate
    upstream.succeed("systemctl stop kea-dhcp6-server.service")
    code = "import json,os; p='/var/lib/kea/test-config.json'; c=json.load(open(p)); c['Dhcp6']['subnet6'][0]['pd-pools']=[{'prefix':" + repr(prefix) + ",'prefix-len':56,'delegated-len':" + str(length) + "}]; c['Dhcp6']['lease-database']['name']='/var/lib/kea/" + lease_name + "'; json.dump(c,open(p+'.next','w')); os.replace(p+'.next',p)"
    upstream.succeed("python3 -c " + shlex.quote(code))
    aggregate = ipaddress.ip_network(prefix + "/56")
    upstream.succeed("ip -6 route replace " + str(aggregate) + " via " + shlex.quote(next_hop) + " dev eth1")
    upstream.succeed("systemctl start kea-dhcp6-server.service")
    upstream.wait_until_succeeds("ss -H -6 -lun sport = :547 | grep -q .", timeout=30)


api("POST", "/session", {"username":"admin","password":"test-password-123"})
cfg = api("GET", "/config")["config"]
cfg["ipv6"] = {"enable":True,"wanMode":"dhcpv6","lanPrefixDelegation":True,"lanRouterAdvertisement":True}
cfg["wifi"] = {
    "enable":True,"bridgeToLan":False,
    "network":{"ipv4Cidr":"192.168.60.1/24","dhcp":{"enable":True,"rangeStart":"192.168.60.100","rangeEnd":"192.168.60.110","leaseMins":30},"allowManagement":False,"allowWiredAccess":False},
    "aps":[{"enable":True,"ssid":"dual-ipv6-test","band":"2g","channel":6,"security":"wpa3-sae","psk":"synthetic-v6-wifi-password"}],
}
if RADIO_ADVANCED:
    cfg["wifi"]["aps"][0].update({"band":"5g","channel":149,"standard":"wifi7","channelWidthMHz":80})
cfg["dns"]["upstreams"] = []
next_hop = next(a["local"] for r in json.loads(router.succeed("ip -j -6 address show eth1")) for a in r["addr_info"] if a["scope"] == "link")
upstream.succeed("ip -6 route replace " + str(aggregate) + " via " + shlex.quote(next_hop) + " dev eth1")
server = "import socket\nfrom http.server import BaseHTTPRequestHandler,HTTPServer\nclass H(BaseHTTPRequestHandler):\n def do_GET(self):\n  self.send_response(200); self.end_headers(); self.wfile.write(self.client_address[0].encode())\n def do_POST(self):\n  data=self.rfile.read(int(self.headers['Content-Length'])); self.send_response(200); self.send_header('Content-Length',str(len(data))); self.end_headers(); self.wfile.write(data)\nclass S(HTTPServer): address_family=socket.AF_INET6\nS(('::',8090),H).serve_forever()\n"
upstream.succeed("python3 -u -c " + shlex.quote(server) + " >/tmp/dual-http.log 2>&1 &")
upstream.wait_for_open_port(8090)

try:
    with subtest("Real PD assigns distinct wired and WPA3 WiFi /64s with DNS and no NAT66"):
        apply(cfg)
        wired_gateway,wifi_gateway,wired_address,wifi_address = wait_dual()
        station4()
        client.fail("ping -6 -c1 -W2 " + wifi_address)
        router.fail(NS + "ping -6 -c1 -W2 " + wired_address)
        router.fail(NS + "nc -6 -z -w2 " + wifi_gateway + " 8080")
        router.fail(NS + "nc -6 -z -w2 " + wifi_gateway + " 22")
        router.wait_until_succeeds("test $(cat /proc/sys/net/ipv6/conf/eth1/mtu) = 1400", timeout=30)
        code = "import http.client; d=bytes(range(256))*256; c=http.client.HTTPConnection('2001:db8:ffff::1',8090,timeout=15); c.request('POST','/',d); r=c.getresponse(); assert r.status==200; assert r.read()==d"
        router.succeed(NS + "python3 -c " + shlex.quote(code))
    with subtest("IPv6 management and cross-subnet permissions explicitly enable and revoke"):
        cfg["wifi"]["network"]["allowManagement"] = True
        cfg["wifi"]["network"]["allowWiredAccess"] = True
        apply(cfg)
        wired_gateway,wifi_gateway,wired_address,wifi_address = wait_dual()
        client.succeed("ping -6 -c1 -W2 " + wifi_address)
        router.succeed(NS + "ping -6 -c1 -W2 " + wired_address)
        router.succeed(NS + "nc -6 -z -w2 " + wifi_gateway + " 8080")
        router.succeed(NS + "nc -6 -z -w2 " + wifi_gateway + " 22")
        cfg["wifi"]["network"]["allowManagement"] = False
        cfg["wifi"]["network"]["allowWiredAccess"] = False
        apply(cfg)
        wired_gateway,wifi_gateway,wired_address,wifi_address = wait_dual()
        client.fail("ping -6 -c1 -W2 " + wifi_address)
        router.fail(NS + "ping -6 -c1 -W2 " + wired_address)
        router.fail(NS + "nc -6 -z -w2 " + wifi_gateway + " 8080")
    with subtest("ISP renumbers real IA_PD automatically across both LANs"):
        isp_delegation("2001:db8:200::",60,"renumber.leases")
        wait_dual()
        for interface in ("br-lan","wlan0"):
            router.fail("ip -6 -o address show " + interface + " | grep '2001:db8:100:'")
    with subtest("Insufficient real /64 PD withdraws both LANs and retains WAN/IPv4"):
        isp_delegation("2001:db8:300::",64,"small.leases")
        for interface in ("br-lan","wlan0"):
            router.wait_until_succeeds("test -z \"$(ip -6 -o address show " + interface + " scope global)\"", timeout=180)
        router.wait_until_succeeds("journalctl -u nixos-router-wan6.service --no-pager | grep -F 'cannot provide two distinct LAN /64s'", timeout=60)
        router.succeed("ping -6 -c1 -W2 2001:db8:ffff::1")
        router.succeed(NS + "ping -4 -c1 -W2 10.0.0.1")
        client.fail("ping -6 -c1 -W2 2001:db8:ffff::1")
        router.fail(NS + "ping -6 -c1 -W2 2001:db8:ffff::1")
        isp_delegation("2001:db8:200::",60,"recovered.leases")
        wait_dual()
    with subtest("DHCPv6 loss naturally expires both delegations and automatically recovers"):
        station4()
        upstream.succeed("systemctl stop kea-dhcp6-server.service")
        for interface in ("br-lan","wlan0"):
            router.wait_until_succeeds("test -z \"$(ip -6 -o address show " + interface + " scope global)\"", timeout=180)
        router.succeed("ip -6 route show default | grep 'proto 242'")
        router.succeed("ping -6 -c1 -W2 2001:db8:ffff::1")
        router.succeed(NS + "ping -4 -c1 -W2 10.0.0.1")
        router.fail(NS + "ping -6 -c1 -W2 2001:db8:ffff::1")
        upstream.succeed("systemctl start kea-dhcp6-server.service")
        wait_dual()
    with subtest("Full router reboot restores both networks and real WPA3/PD traffic"):
        router.reboot()
        router.wait_for_unit("nixos-router-backend.service",timeout=180)
        router.wait_for_unit("dual-radio-station.service")
        wait_dual()
        station4()
    with subtest("Disabling IPv6 withdraws both LANs while WiFi IPv4 remains usable"):
        cfg["ipv6"]["enable"] = False
        apply(cfg)
        for interface in ("br-lan","wlan0"):
            router.succeed("test -z \"$(ip -6 -o address show " + interface + " scope global)\"")
            router.succeed("test $(cat /proc/sys/net/ipv6/conf/" + interface + "/force_forwarding) = 0")
        router.succeed("test -z \"$(ip -6 route show proto 242)\"")
        router.succeed("test $(cat /proc/sys/net/ipv6/conf/eth1/mtu) = 1500")
        router.wait_until_succeeds(NS + "wpa_cli -p /run/dual-station-control -i wlan1 status | grep -Fx wpa_state=COMPLETED",timeout=120)
        station4()
        router.fail(NS + "ping -6 -c1 -W2 2001:db8:ffff::1")
finally:
    for node in (router,upstream):
        print(node.name,node.succeed("ip -6 address show; ip -6 route show; journalctl -u nixos-router-wan6.service -u nixos-router-executor.service -u kea-dhcp6-server.service --no-pager -n 100"))
    print(router.succeed(NS + "ip -6 address show; " + NS + "ip -6 route show"))
