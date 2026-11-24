import json
import shlex
import re
import time

router.start(allow_reboot=True)
start_all()
upstream.wait_for_unit("test-pppoe-server.service")
router.wait_for_unit("nixos-router-backend.service", timeout=180)
client.wait_until_succeeds("ip -4 -o addr show eth1 | grep 'inet 192.168.50.'", timeout=120)
base = "http://192.168.50.1:8080/api/v1"
ppp_password = r'ppp "quoted" \ # secret'


def api(method, path, body=None):
    command = "curl --connect-timeout 3 --max-time 20 -fsS -b /tmp/cookie -c /tmp/cookie -X " + method
    if method in ("PUT", "PATCH"):
        revision = api("GET", "/config")["config"]["revision"]
        command += " -H " + shlex.quote("If-Match: " + json.dumps(revision))
    if body is not None:
        command += " -H 'Content-Type: application/json' --data " + shlex.quote(json.dumps(body))
    return json.loads(client.succeed(command + " " + shlex.quote(base + path)))


api("POST", "/session", {"username": "admin", "password": "test-password-123"})
original = api("GET", "/config")["config"]
cfg = json.loads(json.dumps(original))
cfg["wan"]["mode"] = "pppoe"
cfg["wan"]["pppoe"] = {"username": "router-test", "password": ppp_password}
cfg["dns"]["upstreams"] = []  # Prove IPCP peer DNS flows into the LAN resolver.


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
            payload = result["payload"]
            if isinstance(payload, str):
                payload = json.loads(payload)
            if payload.get("confirmation"):
                confirmation = api("POST", "/apply/confirm", {"id": payload["confirmation"]["id"]})
                assert confirmation["confirmation"]["state"] == "confirmed", confirmation
                payload["stage"] = payload["activationStage"]
            return payload
    raise Exception("configuration transaction timed out")


def connected():
    router.wait_until_succeeds("ip -4 route get 10.0.0.1 | grep 'dev nr-ppp0'", timeout=120)
    client.wait_until_succeeds("ping -c1 -W2 10.0.0.1", timeout=120)
    client.wait_until_succeeds("dig +short @192.168.50.1 test.isp | grep 10.0.0.1", timeout=60)
    router.succeed("grep -Fx 'nameserver 10.0.0.1' /var/lib/nixos-router/managed/wan-resolv.conf")
    links = json.loads(router.succeed("ip -j -4 addr show nr-ppp0"))
    alias = router.succeed("cat /sys/class/net/nr-ppp0/ifalias").strip()
    assert alias == "nixos-router-managed-ppp", (alias, links)
    return next(a["local"] for a in links[0]["addr_info"] if a["family"] == "inet")


with subtest("real PPP negotiation, peer DNS, NAT and credential boundary"):
    payload = apply(cfg)
    assert payload["stage"] in ("complete", "awaiting-pppoe-session"), payload
    address = connected()
    router.succeed("test $(stat -c %a /var/lib/nixos-router/managed/current/pppoe.options) = 600")
    router.fail("runuser -u nobody -- cat /var/lib/nixos-router/managed/current/pppoe.options")
    processes = router.succeed("ps -eo args")
    assert ppp_password not in processes, processes
    journal = router.succeed("journalctl -u nixos-router-pppoe --no-pager")
    assert ppp_password not in journal, journal
    upstream.fail("curl --connect-timeout 2 --max-time 3 http://" + address + ":8080/api/v1/health")
    upstream.fail("nc -z -w2 " + address + " 22")
    api("GET", "/health")

with subtest("PPPoE inbound forwarding uses the logical interface"):
    client_ip = json.loads(client.succeed("ip -j -4 addr show eth1"))[0]["addr_info"][0]["local"]
    client.succeed("systemd-run --unit=test-http python3 -m http.server 9090 --bind 0.0.0.0")
    cfg["firewall"]["portForwards"] = [{"protocol": "tcp", "externalPort": 18080, "destIp": client_ip, "destPort": 9090}]
    apply(cfg)
    address = connected()
    upstream.wait_until_succeeds("curl --connect-timeout 2 --max-time 5 -fsS http://" + address + ":18080/", timeout=60)
    cfg["firewall"]["portForwards"] = []
    apply(cfg)
    address = connected()
    upstream.fail("curl --connect-timeout 2 --max-time 3 http://" + address + ":18080/")

with subtest("unchanged topology preserves STP forwarding and the PPP session"):
    router.succeed("ip link set dev br-lan type bridge stp_state 1")
    router.wait_until_succeeds("test $(cat /sys/class/net/eth2/brport/state) = 3", timeout=40)
    def live_identity():
        return router.succeed("readlink /var/lib/nixos-router/managed/current; "
                              "cat /sys/class/net/br-lan/ifindex /sys/class/net/nr-ppp0/ifindex; "
                              "systemctl show -p MainPID -p NRestarts --value nixos-router-pppoe").strip()
    before = live_identity()
    apply(cfg)
    assert live_identity() == before, "no-op Apply replaced bridge or WAN session"
    router.succeed("test $(cat /sys/class/net/eth2/brport/state) = 3")
    cfg["dns"]["upstreams"] = ["10.0.0.1"]
    apply(cfg)
    assert live_identity() == before, "DNS Apply replaced bridge or WAN session"
    router.succeed("test $(cat /sys/class/net/eth2/brport/state) = 3")
    connected()
    cfg["dns"]["upstreams"] = []
    apply(cfg)
    assert live_identity() == before

with subtest("upstream loss and reconnection preserve LAN management"):
    upstream.succeed("systemctl stop test-pppoe-server")
    router.wait_until_fails("ip -4 route show default | grep nr-ppp0", timeout=90)
    api("GET", "/health")
    client.fail("ping -c1 -W2 10.0.0.1")
    upstream.succeed("systemctl start test-pppoe-server")
    connected()

with subtest("local pppd crash and service restart restore real traffic"):
    old_pid = router.succeed("systemctl show -p MainPID --value nixos-router-pppoe").strip()
    assert int(old_pid) > 0, old_pid
    router.succeed("systemctl kill --kill-whom=main --signal=KILL nixos-router-pppoe")
    api("GET", "/health")
    router.wait_until_succeeds("test $(systemctl show -p MainPID --value nixos-router-pppoe) -gt 0 && test $(systemctl show -p MainPID --value nixos-router-pppoe) != " + old_pid, timeout=60)
    connected()
    router.succeed("systemctl restart nixos-router-pppoe")
    api("GET", "/health")
    connected()

with subtest("wrong password cannot be mistaken for a connected session"):
    cfg["wan"]["pppoe"]["password"] = "deliberately-wrong"
    result = apply(cfg)
    assert result["stage"] == "awaiting-pppoe-session", result
    router.wait_until_succeeds("journalctl -u nixos-router-pppoe --no-pager | grep -Ei 'authentication failed|PAP authentication failed'", timeout=90)
    router.fail("ip -4 route show default | grep nr-ppp0")
    api("GET", "/health")
    cfg["wan"]["pppoe"]["password"] = ppp_password
    apply(cfg)
    connected()

with subtest("switch to DHCP releases PPP and restores the physical exit"):
    previous_generation = router.succeed("readlink /var/lib/nixos-router/managed/current").strip()
    service_command = router.succeed("systemctl show -p ExecStart --value nixos-router-pppoe")
    hook_match = re.search(r"ip-up-script ([^ ;]+)", service_command)
    assert hook_match is not None, service_command
    up_hook = hook_match.group(1)
    apply(original)
    router.wait_until_succeeds("ip -4 route get 10.0.0.1 | grep 'dev eth1'", timeout=120)
    router.fail("systemctl is-active --quiet nixos-router-pppoe")
    router.fail("ip link show nr-ppp0")
    client.wait_until_succeeds("ping -c1 -W2 10.0.0.1", timeout=60)
    router.succeed("test ! -s /var/lib/nixos-router/managed/current/pppoe.options")
    resolver_before = router.succeed("cat /var/lib/nixos-router/managed/wan-resolv.conf")
    router.succeed("env DNS1=203.0.113.99 " + shlex.quote(up_hook) + " nr-ppp0 /dev/null 0 10.77.0.10 10.77.0.1 " + shlex.quote(previous_generation))
    assert router.succeed("cat /var/lib/nixos-router/managed/wan-resolv.conf") == resolver_before
    router.fail("ip -4 route show default | grep nr-ppp0")

with subtest("reboot restores PPP and real client traffic"):
    apply(cfg)
    connected()
    router.reboot()
    router.wait_for_unit("nixos-router-backend.service", timeout=180)
    connected()

with subtest("negotiated lower peer MRU clamps both directions and transfers bulk data"):
    # Keep server MTU 1492 but lower its receive limit: this makes the
    # router's actual transmit MTU 1400 rather than the configured 1492.
    unit = upstream.succeed("systemctl cat test-pppoe-server")
    command_match = re.search(r"^ExecStart=(.+)$", unit, re.MULTILINE)
    assert command_match is not None, unit
    command = command_match.group(1)
    pppd_match = re.search(r" -q (\S+)", command)
    assert pppd_match is not None, command
    pppd = pppd_match.group(1)
    # rp-pppoe appends mru/mtu on the pppd command line, overriding -O.
    # Its documented -q wrapper facility lets the simulated ISP set MRU last.
    wrapper = '#!/bin/sh\nexec ' + shlex.quote(pppd) + ' "$@" mru 1400\n'
    upstream.succeed("printf %s " + shlex.quote(wrapper) + " > /run/low-mru-pppd && chmod 700 /run/low-mru-pppd")
    override = "[Service]\nExecStart=\nExecStart=" + command.replace("-q " + pppd, "-q /run/low-mru-pppd") + "\n"
    upstream.succeed("mkdir -p /run/systemd/system/test-pppoe-server.service.d && printf %s " + shlex.quote(override) + " > /run/systemd/system/test-pppoe-server.service.d/mru.conf")
    upstream.succeed("systemctl daemon-reload && systemctl restart test-pppoe-server")
    router.wait_until_succeeds("test $(cat /sys/class/net/nr-ppp0/mtu) = 1400", timeout=120)
    connected()
    before_pid = router.succeed("systemctl show -p MainPID --value nixos-router-pppoe").strip()
    cfg["firewall"]["blockedMacs"] = ["02:00:00:00:00:01"]
    apply(cfg)
    assert router.succeed("systemctl show -p MainPID --value nixos-router-pppoe").strip() == before_pid
    clamps = router.succeed("nft list chain inet nixos_router ppp_mss")
    assert "maxseg size set 1360" in clamps and "maxseg size set 1340" in clamps, clamps
    # Put the TCP endpoint behind the ISP with Ethernet MTU 1500. A server
    # terminating TCP on the PPP peer can choose a small MSS itself and mask
    # the router's missing clamp.
    upstream.succeed("ip netns add internet")
    upstream.succeed("ip link add isp-end type veth peer name internet-end")
    upstream.succeed("ip link set internet-end netns internet")
    upstream.succeed("ip addr add 10.99.0.1/24 dev isp-end && ip link set isp-end up")
    upstream.succeed("ip -n internet addr add 10.99.0.2/24 dev internet-end && ip -n internet link set internet-end up && ip -n internet link set lo up")
    upstream.succeed("ip -n internet route add default via 10.99.0.1 && sysctl -w net.ipv4.ip_forward=1")
    server = '''import socket
payload = b"0123456789abcdef" * 131072
with socket.socket() as listener:
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.bind(("0.0.0.0", 19090))
    listener.listen()
    connection, _ = listener.accept()
    with connection:
        connection.settimeout(30)
        mss = connection.getsockopt(socket.IPPROTO_TCP, socket.TCP_MAXSEG)
        stream = connection.makefile("rb")
        assert stream.read(len(payload)) == payload
        connection.sendall((str(mss) + "\\n").encode() + payload[::-1])
'''
    upstream.succeed("printf %s " + shlex.quote(server) + " > /run/bulk-server.py")
    upstream.succeed("systemd-run --unit=ppp-bulk ip netns exec internet /run/current-system/sw/bin/python3 /run/bulk-server.py")
    upstream.wait_until_succeeds("ip netns exec internet ss -lnt | grep ':19090 '", timeout=30)
    transfer = '''import socket, json
payload = b"0123456789abcdef" * 131072
with socket.create_connection(("10.99.0.2", 19090), timeout=30) as connection:
    mss = connection.getsockopt(socket.IPPROTO_TCP, socket.TCP_MAXSEG)
    connection.sendall(payload)
    stream = connection.makefile("rb")
    remote_mss = int(stream.readline())
    assert stream.read(len(payload)) == payload[::-1]
    print(json.dumps({"local": mss, "remote": remote_mss}))
'''
    result = json.loads(client.succeed("python3 -c " + shlex.quote(transfer)))
    print("negotiated TCP MSS:", result)
    assert 0 < result["local"] <= 1360 and 0 < result["remote"] <= 1360, result
