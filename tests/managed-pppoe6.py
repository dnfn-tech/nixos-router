import ipaddress
import json
import shlex
import time

router.start(allow_reboot=True)
start_all()
router.wait_for_unit("nixos-router-backend.service", timeout=180)
upstream.wait_for_unit("test-pppoe-server.service")
upstream.wait_for_unit("network-addresses-lo.service")
client.wait_until_succeeds("ip -4 -o addr show eth1 | grep 'inet 192.168.50.'", timeout=120)
BASE = "http://192.168.50.1:8080/api/v1"
PD = ipaddress.IPv6Network("2001:db8:100::/56")


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
            return
    raise AssertionError("PPPoE6 apply timed out")


def connected():
    router.wait_until_succeeds("ip -4 route get 10.0.0.1 | grep 'dev nr-ppp0'", timeout=120)
    upstream.wait_for_unit("test-ppp6-kea.service", timeout=120)
    upstream.wait_for_unit("test-ppp6-ra.service", timeout=120)
    upstream.wait_until_succeeds("ss -H -6 -lun sport = :547 | grep -q .", timeout=30)
    router.wait_until_succeeds("ip -6 route show default | grep 'dev nr-ppp0.*proto 242'", timeout=120)
    # RA/default routing can survive DHCP expiry; server readiness alone
    # does not mean the asynchronous IA_NA exchange has completed again.
    router.wait_until_succeeds("ip -6 -o address show dev nr-ppp0 proto 242 | grep '2001:db8:77::100/128'", timeout=120)
    router.fail("ip -6 -o address show nr-ppp0 | grep 'proto kernel_ra'")
    router.wait_until_succeeds("ip -6 -o address show br-lan scope global | grep '2001:db8:100:'", timeout=120)
    client.wait_until_succeeds("ping -6 -c1 -W2 2001:db8:feed::2", timeout=120)
    client.succeed("ping -4 -c1 -W2 10.99.0.2")
    client.succeed("dig +short @192.168.50.1 test.isp A | grep -Fx 10.0.0.1")
    client.succeed("dig +short @192.168.50.1 v6.test.isp AAAA | grep -Fx 2001:db8:feed::2")
    router.succeed("grep -Fx 'nameserver 10.0.0.1' /var/lib/nixos-router/managed/wan-resolv.conf")
    router.succeed("grep -Fx 'nameserver 2001:db8:ffff::53' /var/lib/nixos-router/managed/wan-resolv.conf")
    router.succeed("test $(cat /sys/class/net/nr-ppp0/mtu) = 1400")
    router.succeed("test $(cat /proc/sys/net/ipv6/conf/nr-ppp0/mtu) = 1400")
    router.succeed("test $(cat /sys/class/net/eth1/mtu) = 1500")
    router.succeed("test $(cat /sys/class/net/nr-ppp0/ifalias) = nixos-router-managed-ppp")
    rows = json.loads(client.succeed("ip -j -6 address show eth1"))
    return next(a["local"] for row in rows for a in row["addr_info"] if a["scope"] == "global" and ipaddress.IPv6Address(a["local"]) in PD)


# The TCP endpoint is behind the ISP on a 1500-byte Ethernet link; terminating
# TCP on the 1400-byte PPP peer would conceal a missing forward-path MSS clamp.
upstream.succeed("ip netns add internet")
upstream.succeed("ip link add isp-end type veth peer name internet-end")
upstream.succeed("ip link set internet-end netns internet")
upstream.succeed("ip link set isp-end up; ip address add 10.99.0.1/24 dev isp-end; ip -6 address add 2001:db8:feed::1/64 dev isp-end")
upstream.succeed("ip -n internet link set lo up; ip -n internet link set internet-end up; ip -n internet address add 10.99.0.2/24 dev internet-end; ip -n internet -6 address add 2001:db8:feed::2/64 dev internet-end")
upstream.succeed("ip -n internet route add default via 10.99.0.1; ip -n internet -6 route add default via 2001:db8:feed::1")
server = '''import socket,json
payload=b"0123456789abcdef"*131072
with socket.socket(socket.AF_INET6) as listener:
 listener.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
 listener.bind(("::",19090)); listener.listen()
 while True:
  connection,peer=listener.accept()
  with connection:
   connection.settimeout(30)
   result={"mss":connection.getsockopt(socket.IPPROTO_TCP,socket.TCP_MAXSEG),"source":peer[0]}
   stream=connection.makefile("rb")
   assert stream.read(len(payload))==payload
   connection.sendall((json.dumps(result)+"\\n").encode()+payload[::-1])
'''
upstream.succeed("printf %s " + shlex.quote(server) + " > /run/ppp6-bulk.py")
upstream.succeed("systemd-run --unit=ppp6-bulk ip netns exec internet /run/current-system/sw/bin/python3 /run/ppp6-bulk.py")
upstream.wait_until_succeeds("ip netns exec internet ss -lnt | grep ':19090 '", timeout=30)
api("POST", "/session", {"username":"admin","password":"test-password-123"})
cfg = api("GET", "/config")["config"]
ppp_password = r'ppp "quoted" \ # secret'
cfg["wan"]["mode"] = "pppoe"
cfg["wan"]["pppoe"] = {"username":"router-test","password":ppp_password}
cfg["ipv6"] = {"enable":True,"wanMode":"pppoe6","lanPrefixDelegation":True,"lanRouterAdvertisement":True}
cfg["dns"]["upstreams"] = []
try:
    with subtest("Real PPPoE IPv6CP obtains IA_NA/PD, RA, both DNS sources and dual-stack traffic"):
        apply(cfg)
        connected()
        router.succeed("test $(stat -c %a /var/lib/nixos-router/managed/current/pppoe.options) = 600")
        router.succeed("grep -Fx '+ipv6' /var/lib/nixos-router/managed/current/pppoe.options")
        rows = json.loads(router.succeed("ip -j -6 address show nr-ppp0"))
        address = next(a["local"] for row in rows for a in row["addr_info"] if a["scope"] == "global")
        upstream.fail("curl -g --connect-timeout 2 --max-time 3 'http://[" + address + "]:8080/api/v1/health'")
        upstream.fail("nc -6 -z -w2 " + address + " 22")
    with subtest("Negotiated MTU clamps both real TCP directions; IPv6 forwarding has no NAT66"):
        for endpoint, limit in (("2001:db8:feed::2",1340),("10.99.0.2",1360)):
            transfer = '''import socket,json
payload=b"0123456789abcdef"*131072
with socket.create_connection((ENDPOINT,19090),timeout=30) as connection:
 local=connection.getsockopt(socket.IPPROTO_TCP,socket.TCP_MAXSEG)
 connection.sendall(payload); stream=connection.makefile("rb")
 result=json.loads(stream.readline())
 assert stream.read(len(payload))==payload[::-1]
 result["local"]=local
 print(json.dumps(result))
'''.replace("ENDPOINT",repr(endpoint))
            result = json.loads(client.succeed("python3 -c " + shlex.quote(transfer)))
            assert 0 < result["local"] <= limit and 0 < result["mss"] <= limit, result
            if ":" in endpoint:
                assert ipaddress.IPv6Address(result["source"]) in PD, result
    with subtest("DHCPv6 loss naturally expires PPP LAN delegation while IPv4 and RA survive"):
        upstream.succeed("systemctl stop test-ppp6-kea.service")
        router.wait_until_succeeds("test -z \"$(ip -6 -o address show br-lan scope global)\"", timeout=180)
        router.succeed("ping -6 -c1 -W2 2001:db8:ffff::53")
        client.succeed("ping -4 -c1 -W2 10.99.0.2")
        client.fail("ping -6 -c1 -W2 2001:db8:feed::2")
        upstream.succeed("systemctl start test-ppp6-kea.service")
        connected()
    with subtest("PPP peer loss withdraws IPv6 resources and reconnect restores real traffic"):
        upstream.succeed("systemctl stop test-pppoe-server.service")
        router.wait_until_succeeds("test -z \"$(ip -6 -o address show br-lan scope global)\"", timeout=90)
        router.wait_until_fails("systemctl is-active --quiet nixos-router-wan6.service", timeout=90)
        api("GET", "/health")
        client.fail("ping -6 -c1 -W2 2001:db8:feed::2")
        upstream.succeed("systemctl start test-pppoe-server.service")
        connected()
    with subtest("CPE pppd crash and restart restore the PPP IPv6CP child service"):
        old = router.succeed("systemctl show -p MainPID --value nixos-router-pppoe.service").strip()
        router.succeed("systemctl kill --kill-whom=main --signal=KILL nixos-router-pppoe.service")
        router.wait_until_succeeds("test $(systemctl show -p MainPID --value nixos-router-pppoe.service) -gt 0 && test $(systemctl show -p MainPID --value nixos-router-pppoe.service) != " + old, timeout=60)
        connected()
    with subtest("Full reboot restores PPP dual-stack last-good state"):
        router.reboot()
        router.wait_for_unit("nixos-router-backend.service", timeout=180)
        connected()
    with subtest("Disable IPv6 retains IPv4 PPP and withdraws owned IPv6 resources"):
        cfg["ipv6"]["enable"] = False
        apply(cfg)
        router.wait_until_succeeds("ip -4 route get 10.0.0.1 | grep 'dev nr-ppp0'", timeout=120)
        client.wait_until_succeeds("ping -4 -c1 -W2 10.99.0.2", timeout=120)
        router.succeed("test -z \"$(ip -6 -o address show br-lan scope global)\"")
        router.succeed("test -z \"$(ip -6 route show proto 242)\"")
        router.fail("systemctl is-active --quiet nixos-router-wan6.service")
        router.fail("grep -Fx 'nameserver 2001:db8:ffff::53' /var/lib/nixos-router/managed/wan-resolv.conf")
    with subtest("Switch from PPP to Ethernet DHCP leaves no logical IPv6 state"):
        cfg["wan"]["mode"] = "dhcp"
        cfg["ipv6"]["wanMode"] = "disabled"
        apply(cfg)
        router.wait_until_succeeds("ip -4 route get 10.0.0.1 | grep 'dev eth1'", timeout=120)
        client.wait_until_succeeds("ping -4 -c1 -W2 10.0.0.1", timeout=120)
        router.fail("ip link show nr-ppp0")
        router.succeed("test -z \"$(ip -6 route show proto 242)\"")
finally:
    print(router.execute("sysctl net.ipv6.conf.nr-ppp0.accept_ra net.ipv6.conf.nr-ppp0.autoconf net.ipv6.conf.nr-ppp0.use_tempaddr; cat /var/lib/nixos-router/managed/current/wan6.env; cat /var/lib/nixos-router/managed/ipv6-resources.json"))
    print(router.succeed("ip -6 address show; ip -6 route show; journalctl -u nixos-router-pppoe.service -u nixos-router-wan6.service -u nixos-router-executor.service --no-pager -n 120"))
    print(upstream.succeed("ip -6 address show; ip -6 route show; journalctl -u test-pppoe-server.service -u test-ppp6-kea.service -u test-ppp6-ra.service --no-pager -n 120"))
