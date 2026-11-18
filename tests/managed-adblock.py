import copy
import json
import shlex
import time

router.start(allow_reboot=True)
start_all()
router.wait_for_unit("nixos-router-backend.service", timeout=180)
upstream.wait_for_unit("dnsmasq.service")
client.wait_until_succeeds("ip -4 -o addr show dev eth1 | grep 'inet 192.168.50.'", timeout=120)
client.wait_until_succeeds("dig +short @192.168.50.1 ads.example A | grep -Fx 10.0.0.9", timeout=120)
base = "http://192.168.50.1:8080/api/v1"


def api(method, path, body=None, status=200):
    cmd = "curl --connect-timeout 3 --max-time 20 -sS -b /tmp/plugin-cookie -c /tmp/plugin-cookie -X " + method
    if method in ("PUT", "PATCH"):
        rev = api("GET", "/config")["config"]["revision"]
        cmd += " -H " + shlex.quote("If-Match: " + json.dumps(rev))
    if body is not None:
        cmd += " -H 'Content-Type: application/json' --data " + shlex.quote(json.dumps(body))
    raw = client.succeed(cmd + " -w '\n%{http_code}' " + shlex.quote(base + path))
    payload, code = raw.rsplit("\n", 1)
    assert int(code) == status, raw
    return json.loads(payload)


def apply(cfg):
    api("PUT", "/config", cfg)
    job = api("POST", "/apply", status=202)
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        result = api("GET", "/jobs/" + job["jobId"])
        if result["status"] in ("success", "failed"):
            assert result["status"] == "success", result
            payload = result["payload"]
            if isinstance(payload, str):
                payload = json.loads(payload)
            if payload.get("confirmation"):
                confirmation = api("POST", "/apply/confirm", {"id": payload["confirmation"]["id"]})
                assert confirmation["confirmation"]["state"] == "confirmed", confirmation
            return
        time.sleep(1)
    raise Exception("module apply timed out")


def resolves(domain, a, aaaa):
    for kind, expected in (("A", a), ("AAAA", aaaa)):
        client.wait_until_succeeds("dig +short @192.168.50.1 " + shlex.quote(domain) + " " + kind + " | grep -Fx " + shlex.quote(expected), timeout=90)
    client.succeed("dig +short @192.168.50.1 test.isp A | grep -Fx 10.0.0.1")


api("POST", "/session", {"username": "admin", "password": "test-password-123"}, status=201)
cfg = api("GET", "/config")["config"]
assert "ssh" not in cfg
with subtest("real A/AAAA DNS through router and system-owned SSH"):
    resolves("ads.example", "10.0.0.9", "2001:db8::9")
    router.succeed("systemctl is-active sshd.service; ss -ltn | grep ':2223 '")

with subtest("module configure apply and actual domain/subdomain blocking"):
    cfg["plugins"] = {"adblock": {"enable": True, "config": {"entries": ["Ads.Example."]}}}
    apply(cfg)
    resolves("ads.example", "0.0.0.0", "::")
    resolves("sub.ads.example", "0.0.0.0", "::")
    resolves("second.example", "10.0.0.10", "2001:db8::10")
    manifest = api("GET", "/plugins/adblock")["plugin"]
    assert manifest["appliedEnabled"] and manifest["runtimeSupported"], manifest

with subtest("invalid rules and DNS dependency fail without changing runtime"):
    for mutate in ("rule", "dns", "lists", "unknown"):
        bad = copy.deepcopy(api("GET", "/config")["config"])
        if mutate == "rule":
            bad["plugins"]["adblock"]["config"]["entries"] = ["bad\nserver=10.0.0.8"]
        elif mutate == "dns":
            bad["dns"]["enableDnsmasq"] = False
        elif mutate == "lists":
            bad["plugins"]["adblock"]["config"]["lists"] = ["https://invalid.example/list"]
        else:
            bad["plugins"]["unknown"] = {"enable": True}
        result = api("PUT", "/config", bad, status=400)
        assert result["fieldErrors"], result
        resolves("ads.example", "0.0.0.0", "::")

with subtest("rule replacement cleans up old rules"):
    cfg = api("GET", "/config")["config"]
    cfg["plugins"]["adblock"]["config"]["entries"] = ["second.example"]
    apply(cfg)
    resolves("ads.example", "10.0.0.9", "2001:db8::9")
    resolves("second.example", "0.0.0.0", "::")

with subtest("executor and whole-machine reboot restore applied rules"):
    router.succeed("systemctl restart nixos-router-executor.service")
    router.wait_for_unit("nixos-router-executor.service")
    resolves("second.example", "0.0.0.0", "::")
    router.reboot()
    router.wait_for_unit("nixos-router-backend.service", timeout=180)
    resolves("second.example", "0.0.0.0", "::")
    api("POST", "/session", {"username": "admin", "password": "test-password-123"}, status=201)
    router.succeed("systemctl is-active sshd.service; ss -ltn | grep ':2223 '")

with subtest("disable restores upstream DNS"):
    cfg = api("GET", "/config")["config"]
    cfg["plugins"]["adblock"]["enable"] = False
    apply(cfg)
    resolves("second.example", "10.0.0.10", "2001:db8::10")
