import json
import shlex
import time

router.start(allow_reboot=True)
start_all()
router.wait_for_unit("nixos-router-backend.service", timeout=240)
router.wait_for_unit("sshd.service")
router.succeed("systemctl is-active nixos-router-management-guard.service")
router.succeed("systemctl show -p Before --value nixos-router-management-guard.service | grep sshd.service")
base = "http://10.66.0.1:8080/api/v1"

def request(node, method, path, body=None, endpoint=None, expected=200):
    url = (endpoint or base) + path
    cmd = "curl --noproxy '*' --connect-timeout 3 --max-time 15 -sS -b /tmp/cookie -c /tmp/cookie -X " + method
    if body is not None:
        cmd += " -H 'Content-Type: application/json' --data " + shlex.quote(json.dumps(body))
    if method == "PUT":
        revision = request(node, "GET", "/config", endpoint=endpoint)["config"]["revision"]
        cmd += " -H " + shlex.quote("If-Match: " + json.dumps(revision))
    raw = node.succeed(cmd + " -w '\n%{http_code}' " + shlex.quote(url))
    data, status = raw.rsplit("\n", 1)
    assert int(status) == expected, (status, data)
    return json.loads(data)

def api(method, path, body=None, expected=200):
    return request(overlay, method, path, body, expected=expected)

def wait_state(state, timeout=120):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status = api("GET", "/apply/candidate")["confirmation"]
        if status and status["state"] == state:
            return status
        time.sleep(1)
    raise Exception("candidate did not reach " + state)

def submit(cidr, firewall=True):
    cfg = api("GET", "/config")["config"]
    subnet = cidr.rsplit(".", 1)[0]
    cfg["lan"]["ipv4Cidr"] = cidr + "/24"
    cfg["lan"]["dhcp"]["rangeStart"] = subnet + ".100"
    cfg["lan"]["dhcp"]["rangeEnd"] = subnet + ".150"
    cfg["firewall"]["enable"] = firewall
    api("PUT", "/config", cfg)
    job = api("POST", "/apply", expected=202)
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        result = api("GET", "/jobs/" + job["jobId"])
        if result["status"] == "failed":
            raise Exception(result)
        if result["status"] == "success":
            payload = json.loads(result["payload"])
            assert payload["stage"] == "awaiting-confirmation", result
            return payload["confirmation"]
        time.sleep(1)
    raise Exception("apply timeout")

def baseline_hash():
    return router.succeed("sha256sum /var/lib/nixos-router/managed/last-good.json").split()[0]

def web(node, url):
    return node.succeed("curl --noproxy '*' --connect-timeout 3 --max-time 5 -fsS " + shlex.quote(url + "/api/v1/health"))

def denied(node, url, source=None):
    cmd = "curl --noproxy '*' --connect-timeout 2 --max-time 3 -fsS "
    if source:
        cmd += "--interface " + source + " "
    node.fail(cmd + shlex.quote(url + "/api/v1/health"))

with subtest("Explicit external management grants; WAN and unauthorized sources denied"):
    overlay.wait_until_succeeds("curl --noproxy '*' -fsS http://10.66.0.1:8080/api/v1/health")
    web(overlay, "http://[fd66::1]:8080")
    web(client, "http://192.168.50.1:8080")
    overlay.succeed("python3 -c \"import socket; s=socket.create_connection(('10.66.0.1',22),3); assert s.recv(128).startswith(b'SSH-')\"")
    overlay.succeed("python3 -c \"import socket; s=socket.create_connection(('fd66::1',22),3); assert s.recv(128).startswith(b'SSH-')\"")
    denied(upstream, "http://10.0.0.2:8080")
    upstream.fail("nc -z -w 2 10.0.0.2 22")
    # Both families and LAN destination via WAN ingress remain rejected.
    router.succeed("ip -6 addr add fd01::2/64 dev eth1")
    denied(upstream, "http://[fd01::2]:8080")
    upstream.fail("nc -6 -z -w 2 fd01::2 22")
    upstream.succeed("ip route add 192.168.50.0/24 via 10.0.0.2")
    denied(upstream, "http://192.168.50.1:8080")
    overlay.succeed("ip addr add 10.66.0.3/24 dev eth1; ip -6 addr add fd66::3/64 dev eth1")
    denied(overlay, "http://10.66.0.1:8080", "10.66.0.3")
    denied(overlay, "http://[fd66::1]:8080", "fd66::3")
    overlay.fail("nc -s 10.66.0.3 -z -w 2 10.66.0.1 22")
    overlay.fail("nc -6 -s fd66::3 -z -w 2 fd66::1 22")
    # Linux selects the newest same-prefix IPv6 address (fd66::3) by default.
    # The negative-case alias must not leak into later authorized probes.
    print(overlay.succeed("ip -6 route get fd66::1"))
    overlay.succeed("ip addr del 10.66.0.3/24 dev eth1; ip -6 addr del fd66::3/64 dev eth1")
    overlay.succeed("ip -6 route get fd66::1 | grep 'src fd66::2'")
    web(overlay, "http://[fd66::1]:8080")
    api("POST", "/session", {"username": "admin", "password": "test-password-123"}, expected=201)

with subtest("LAN renumbering remains reachable and requires explicit confirmation"):
    original = baseline_hash()
    pending = submit("192.168.60.1", firewall=False)
    assert baseline_hash() == original
    denied(upstream, "http://10.0.0.2:8080")
    upstream.fail("nc -z -w 2 10.0.0.2 22")
    web(overlay, "http://10.66.0.1:8080")
    client.succeed("ip -4 addr flush dev eth1; ip addr add 192.168.60.2/24 dev eth1")
    web(client, "http://192.168.60.1:8080")
    # API crash must preserve executor candidate and watchdog, never auto-confirm.
    router.succeed("systemctl restart nixos-router-backend.service")
    router.wait_for_unit("nixos-router-backend.service")
    assert api("GET", "/apply/candidate")["confirmation"]["id"] == pending["id"]
    new_base = "http://192.168.60.1:8080/api/v1"
    request(client, "POST", "/session", {"username": "admin", "password": "test-password-123"}, endpoint=new_base, expected=201)
    for _ in range(2):
        result = request(client, "POST", "/apply/confirm", {"id": pending["id"]}, endpoint=new_base)
        assert result["confirmation"]["state"] == "confirmed"
    confirmed = baseline_hash()
    assert confirmed != original
    router.succeed("systemctl restart nixos-router-executor.service")
    router.wait_for_unit("nixos-router-executor.service", timeout=180)
    web(client, "http://192.168.60.1:8080")
    assert baseline_hash() == confirmed

with subtest("Valid unreachable LAN candidate times out even with API stopped"):
    pending = submit("192.168.70.1", firewall=False)
    denied(client, "http://192.168.60.1:8080")
    router.succeed("systemctl stop nixos-router-backend.service")
    router.wait_until_succeeds("python3 -c \"import json; assert json.load(open('/var/lib/nixos-router/managed/candidate.json'))['state']=='rolled-back'\"", timeout=120)
    assert baseline_hash() == confirmed
    router.succeed("systemctl start nixos-router-backend.service")
    router.wait_for_unit("nixos-router-backend.service")
    web(client, "http://192.168.60.1:8080")
    api("POST", "/apply/confirm", {"id": pending["id"]}, expected=409)

with subtest("Executor death restores unconfirmed candidate before readiness"):
    pending = submit("192.168.80.1", firewall=False)
    router.succeed("systemctl kill --kill-whom=main --signal=SIGKILL nixos-router-executor.service")
    router.wait_until_succeeds("systemctl is-active nixos-router-executor.service", timeout=180)
    wait_state("rolled-back")
    web(client, "http://192.168.60.1:8080")
    assert baseline_hash() == confirmed
    api("POST", "/apply/confirm", {"id": pending["id"]}, expected=409)

with subtest("System reboot restores previous confirmed LAN, not unconfirmed draft"):
    pending = submit("192.168.85.1", firewall=False)
    router.reboot()
    router.wait_for_unit("nixos-router-backend.service", timeout=240)
    wait_state("rolled-back")
    web(client, "http://192.168.60.1:8080")
    web(overlay, "http://[fd66::1]:8080")
    assert baseline_hash() == confirmed
    api("POST", "/apply/confirm", {"id": pending["id"]}, expected=409)

with subtest("Rollback failure remains blocked and restart retries recovery"):
    submit("192.168.90.1", firewall=False)
    router.succeed("mkdir -p /run/systemd/system/nixos-router-dnsmasq.service.d; printf '[Service]\\nExecStartPre=/bin/false\\n' > /run/systemd/system/nixos-router-dnsmasq.service.d/fail.conf; systemctl daemon-reload")
    wait_state("rollback-failed", timeout=150)
    cfg = api("GET", "/config")["config"]
    job = api("POST", "/apply", expected=202)
    router.wait_until_succeeds("true")
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        result = api("GET", "/jobs/" + job["jobId"])
        if result["status"] == "failed":
            break
        time.sleep(1)
    else:
        raise Exception("new apply was not blocked after recovery failure")
    router.succeed("rm /run/systemd/system/nixos-router-dnsmasq.service.d/fail.conf; systemctl daemon-reload; systemctl reset-failed; systemctl restart nixos-router-executor.service")
    router.wait_for_unit("nixos-router-executor.service", timeout=180)
    wait_state("rolled-back")
    assert baseline_hash() == confirmed
    web(client, "http://192.168.60.1:8080")
    denied(upstream, "http://10.0.0.2:8080")
