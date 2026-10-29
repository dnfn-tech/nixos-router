import json
import shlex
import time

router.start(allow_reboot=True)
start_all()
upstream.wait_for_unit("dnsmasq.service")
router.wait_for_unit("nixos-router-backend.service", timeout=180)
router.wait_until_succeeds("ip -4 -o addr show dev eth1 | grep 'inet 10.0.0.'", timeout=120)
for node in [client, observer]:
    node.wait_until_succeeds("ip -4 -o addr show dev eth1 | grep 'inet 192.168.50.'", timeout=120)
    node.succeed("ping -c1 -W2 10.0.0.1")
    node.succeed("dig +short @192.168.50.1 test.isp | grep 10.0.0.1")

base = "http://192.168.50.1:8080/api/v1"
def api(method, path, body=None):
    args = "curl --connect-timeout 3 --max-time 20 -fsS -b /tmp/cookie -c /tmp/cookie -X " + method
    if body is not None:
        args += " -H 'Content-Type: application/json' --data " + shlex.quote(json.dumps(body))
    return json.loads(client.succeed(args + " " + shlex.quote(base + path)))

api("POST", "/session", {"username": "admin", "password": "test-password-123"})
health = api("GET", "/health")
assert health["deployment"]["runtimeApply"] is True, health
cfg = api("GET", "/config")["config"]

def apply(config, success=True):
    api("PUT", "/config", config)
    job = api("POST", "/apply")
    assert job["appliedRuntime"] is False, job
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        time.sleep(1)
        rc, raw = client.execute("curl --connect-timeout 2 --max-time 5 -fsS -b /tmp/cookie " + shlex.quote(base + "/jobs/" + job["jobId"]))
        if rc in (7, 28, 52, 56):
            continue  # LAN activation may interrupt this read; never repeat POST.
        assert rc == 0, (rc, raw)
        result = json.loads(raw)
        if result["status"] != "running":
            assert (result["status"] == "success") == success, result
            payload = json.loads(result["payload"]) if isinstance(result["payload"], str) else result["payload"]
            if success:
                assert payload["stage"] in ("complete", "awaiting-wan-lease"), result
                if payload["stage"] == "awaiting-wan-lease":
                    assert payload["warnings"], result
            # A successful configuration transaction can still be acquiring WAN DHCP.
            # Prove the route/lease independently before asserting forwarded traffic.
            router.wait_until_succeeds("ip -4 route get 10.0.0.1 | grep -E 'dev eth1 .*src 10[.]0[.]0[.]'", timeout=90)
            return result
    raise Exception("apply did not complete")

print(router.succeed("ip -j -d link show"))
# Repeated activation must be idempotent and continue passing real traffic.
apply(cfg)
apply(cfg)
client.succeed("ping -c1 -W2 10.0.0.1")
router.succeed("test $(nft list tables | grep -c 'inet nixos_router') = 1")
wan = router.succeed("ip -j -4 addr show dev eth1")
wan_ip = json.loads(wan)[0]["addr_info"][0]["local"]
upstream.fail("curl --connect-timeout 2 --max-time 3 http://" + wan_ip + ":8080/api/v1/health")
# The API listens dual-stack; WAN IPv6 must be denied by inet filtering too.
wan6_info = json.loads(router.succeed("ip -j -6 address show eth1"))[0]["addr_info"]
wan6 = next(a["local"] for a in wan6_info if a["scope"] == "link")
upstream.fail("curl --noproxy '*' --connect-timeout 2 --max-time 3 'http://[" + wan6 + "%25eth1]:8080/api/v1/health'")
router.succeed("curl --noproxy '*' -g -fsS 'http://[::1]:8080/api/v1/health'")


# Disabling the optional firewall must not expose either management family.
cfg["firewall"]["enable"] = False
apply(cfg)
client.succeed("ping -c1 -W2 10.0.0.1")
upstream.fail("curl --connect-timeout 2 --max-time 3 http://" + wan_ip + ":8080/api/v1/health")
upstream.fail("curl --noproxy '*' --connect-timeout 2 --max-time 3 'http://[" + wan6 + "%25eth1]:8080/api/v1/health'")
# Protect actual NixOS listeners even though saved SSH port deliberately differs.
router.wait_for_unit("sshd.service")
lan6_info = json.loads(router.succeed("ip -j -6 address show br-lan"))[0]["addr_info"]
lan6 = next(a["local"] for a in lan6_info if a["scope"] == "link")
for ssh_port in (22, 2223, 2224):
    client.succeed("nc -z -w 3 192.168.50.1 " + str(ssh_port))
    upstream.fail("nc -z -w 2 " + wan_ip + " " + str(ssh_port))
    client.succeed("nc -6 -z -w 3 " + shlex.quote(lan6 + "%eth1") + " " + str(ssh_port))
    upstream.fail("nc -6 -z -w 2 " + shlex.quote(wan6 + "%eth1") + " " + str(ssh_port))
cfg["firewall"]["enable"] = True
apply(cfg)

# A server behind the router is reachable only while a DNAT rule is applied.
client_ip = json.loads(client.succeed("ip -j -4 addr show dev eth1"))[0]["addr_info"][0]["local"]
client.succeed("mkdir -p /tmp/www; echo forward-ok > /tmp/www/index.html; systemd-run --unit=test-http python3 -m http.server 8088 --directory /tmp/www")
client.wait_for_open_port(8088)
cfg["firewall"]["portForwards"] = [{"protocol":"tcp", "externalPort":18088,"destIp":client_ip,"destPort":8088}]
apply(cfg)
upstream.succeed("curl -fsS --max-time 5 http://" + wan_ip + ":18088 | grep forward-ok")
cfg["firewall"]["portForwards"] = []
apply(cfg)
upstream.fail("curl -fsS --max-time 3 http://" + wan_ip + ":18088")

# Block only one MAC's routed traffic; API management stays accessible.
mac = client.succeed("cat /sys/class/net/eth1/address").strip()
cfg["firewall"]["blockedMacs"] = [mac]
apply(cfg)
client.fail("ping -c1 -W2 10.0.0.1")
observer.succeed("ping -c1 -W2 10.0.0.1")
cfg["firewall"]["blockedMacs"] = []
apply(cfg)
client.succeed("ping -c1 -W2 10.0.0.1")

# An unsupported configuration fails without disturbing the last good network.
cfg["qos"]["enable"] = True
apply(cfg, success=False)
client.succeed("ping -c1 -W2 10.0.0.1")
cfg["qos"]["enable"] = False
apply(cfg)


# Observe source addresses, so disabling NAT is tested independently of routing.
upstream.succeed("ip route replace 192.168.50.0/24 via " + wan_ip)
echo_script = "from http.server import BaseHTTPRequestHandler,HTTPServer\nclass H(BaseHTTPRequestHandler):\n def do_GET(self):\n  self.send_response(200); self.end_headers(); self.wfile.write(self.client_address[0].encode())\nHTTPServer(('0.0.0.0',8090),H).serve_forever()\n"
upstream.succeed("printf %s " + shlex.quote(echo_script) + " > /tmp/echo.py; systemd-run --unit=source-echo python3 /tmp/echo.py")
upstream.wait_for_open_port(8090)
assert client.succeed("curl -fsS http://10.0.0.1:8090").strip() == wan_ip
cfg["firewall"]["natEnabled"] = False
apply(cfg)
assert client.succeed("curl -fsS http://10.0.0.1:8090").strip() == client_ip
cfg["firewall"]["natEnabled"] = True
apply(cfg)
assert client.succeed("curl -fsS http://10.0.0.1:8090").strip() == wan_ip

# Replace the DNS upstream and check an actual answer, not generated text.
upstream.succeed("ip address add 10.0.0.2/24 dev eth1")
upstream.succeed("systemd-run --unit=alternate-dns dnsmasq --keep-in-foreground --port=53 --listen-address=10.0.0.2 --bind-interfaces --no-resolv --address=/test.isp/10.0.0.2")
upstream.wait_for_unit("alternate-dns.service")
cfg["dns"]["upstreams"] = ["10.0.0.2"]
apply(cfg)
client.succeed("dig +short @192.168.50.1 test.isp | grep -Fx 10.0.0.2")
cfg["dns"]["upstreams"] = ["10.0.0.1"]
apply(cfg)

# A real service startup failure after network mutation must recover last-good.
router.succeed("mkdir -p /run/systemd/system/nixos-router-dnsmasq.service.d")
fail_once = "[Service]\nExecStartPre=/bin/sh -c 'if test -e /run/fail-dns-once; then rm /run/fail-dns-once; exit 1; fi'\n"
router.succeed("printf %s " + shlex.quote(fail_once) + " > /run/systemd/system/nixos-router-dnsmasq.service.d/fault.conf; systemctl daemon-reload; touch /run/fail-dns-once")
previous = router.succeed("sha256sum /var/lib/nixos-router/managed/last-good.json").split()[0]
cfg["dns"]["domain"] = "changed.lan"
failed = apply(cfg, success=False)
payload = failed["payload"]
if isinstance(payload, str): payload = json.loads(payload)
assert payload["rollbackStatus"] == "succeeded", failed
assert router.succeed("sha256sum /var/lib/nixos-router/managed/last-good.json").split()[0] == previous
client.succeed("ping -c1 -W2 10.0.0.1")
client.succeed("dig +short @192.168.50.1 test.isp | grep -Fx 10.0.0.1")
router.succeed("rm /run/systemd/system/nixos-router-dnsmasq.service.d/fault.conf; systemctl daemon-reload")
cfg["dns"]["domain"] = "lan"
apply(cfg)

# DHCP pool changes affect a fresh lease on the second LAN client.
cfg["lan"]["dhcp"]["rangeStart"] = "192.168.50.160"
cfg["lan"]["dhcp"]["rangeEnd"] = "192.168.50.170"
apply(cfg)
observer.succeed("systemctl stop systemd-networkd; ip address flush dev eth1; rm -f /run/systemd/netif/leases/*; systemctl start systemd-networkd")
observer.wait_until_succeeds("ip -4 -o address show eth1 | grep -E '192[.]168[.]50[.](16[0-9]|170)/24'", timeout=90)
observer.succeed("ping -c1 -W2 10.0.0.1")

# DHCP-provided DNS must be readable after atomic hook updates.
cfg["dns"]["upstreams"] = []
apply(cfg)
client.wait_until_succeeds("dig +short @192.168.50.1 test.isp | grep -Fx 10.0.0.1", timeout=30)

# Switch WAN to static, then replay an old DHCP callback: it must do nothing.
router.succeed("cp /var/lib/nixos-router/managed/current/wan.env /tmp/old-wan.env")
unit = router.succeed("systemctl cat nixos-router-wan.service")
import re
hook_match = re.search(r"-s (/nix/store/[^ ]+-routerd-dhcp-hook)", unit)
assert hook_match is not None, unit
hook = hook_match.group(1)
cfg["wan"] = {"interface":"eth1", "mode":"static", "static":{"addressCidr":"10.0.0.10/24", "gateway":"10.0.0.1", "dns":["10.0.0.1"]}}
apply(cfg)
router.succeed("ip -4 -o address show eth1 | grep '10.0.0.10/24'")
router.succeed("set -a; . /tmp/old-wan.env; interface=eth1 ip=10.0.0.199 subnet=255.255.255.0 router=10.0.0.1 dns=10.0.0.2 " + hook + " bound")
router.succeed("ip -4 -o address show eth1 | grep '10.0.0.10/24'")
router.fail("ip -4 -o address show eth1 | grep '10.0.0.199'")
client.succeed("ping -c1 -W2 10.0.0.1")
client.succeed("dig +short @192.168.50.1 test.isp | grep -Fx 10.0.0.1")
cfg["wan"] = {"interface":"eth1", "mode":"dhcp"}
cfg["dns"]["upstreams"] = ["10.0.0.1"]
apply(cfg)
router.wait_until_succeeds("ip -4 -o address show eth1 | grep '10.0.0.1[0-9][0-9]/24'", timeout=60)
client.succeed("ping -c1 -W2 10.0.0.1")

# Crash after a candidate adds a LAN member, before last-good persistence.
router.succeed("mkdir -p /run/systemd/system/nixos-router-dnsmasq.service.d")
pause_once = "[Service]\nExecStartPre=/bin/sh -c 'if test -e /run/pause-once; then rm /run/pause-once; touch /run/activation-paused; sleep 120; fi'\n"
router.succeed("printf %s " + shlex.quote(pause_once) + " > /run/systemd/system/nixos-router-dnsmasq.service.d/pause.conf; systemctl daemon-reload; touch /run/pause-once")
last_good_hash = router.succeed("sha256sum /var/lib/nixos-router/managed/last-good.json").split()[0]
candidate = json.loads(json.dumps(cfg))
candidate["lan"]["ports"] = ["eth2", "eth3"]
api("PUT", "/config", candidate)
interrupted_job = api("POST", "/apply")["jobId"]
router.wait_until_succeeds("test -e /run/activation-paused && test $(basename $(readlink /sys/class/net/eth3/master)) = br-lan", timeout=60)
router.succeed("systemctl kill --kill-whom=all --signal=SIGKILL nixos-router-backend.service")
client.wait_until_succeeds("curl -fsS http://192.168.50.1:8080/api/v1/health", timeout=90)
router.succeed("test ! -e /sys/class/net/eth3/master")
assert router.succeed("sha256sum /var/lib/nixos-router/managed/last-good.json").split()[0] == last_good_hash
assert api("GET", "/jobs/" + interrupted_job)["status"] == "failed"
router.wait_until_succeeds("ip -4 route get 10.0.0.1 | grep 'dev eth1'", timeout=90)
client.succeed("ping -c1 -W2 10.0.0.1")
router.succeed("rm /run/systemd/system/nixos-router-dnsmasq.service.d/pause.conf; systemctl daemon-reload")
api("PUT", "/config", cfg)

# A saved draft must never become runtime state merely through a daemon restart.
draft = json.loads(json.dumps(cfg))
draft["lan"]["ipv4Cidr"] = "192.168.77.1/24"
draft["lan"]["dhcp"]["rangeStart"] = "192.168.77.100"
draft["lan"]["dhcp"]["rangeEnd"] = "192.168.77.150"
api("PUT", "/config", draft)
router.succeed("systemctl restart nixos-router-backend")
router.wait_for_unit("nixos-router-backend.service")
client.wait_until_succeeds("ping -c1 -W2 192.168.50.1")
router.fail("ip -4 addr show br-lan | grep 192.168.77.1")
client.succeed("ping -c1 -W2 10.0.0.1")
# Abrupt daemon death also restores last-good, not the saved draft.
router.succeed("systemctl kill --kill-whom=main --signal=SIGKILL nixos-router-backend.service")
router.wait_for_unit("nixos-router-backend.service", timeout=90)
client.wait_until_succeeds("curl -fsS http://192.168.50.1:8080/api/v1/health")
router.fail("ip -4 addr show br-lan | grep 192.168.77.1")
client.succeed("ping -c1 -W2 10.0.0.1")
# Even syntactically corrupt draft files must not prevent last-good recovery.
router.succeed("printf '{broken' > /var/lib/nixos-router/config.json; systemctl restart nixos-router-backend")
router.wait_for_unit("nixos-router-backend.service")
client.wait_until_succeeds("curl -fsS http://192.168.50.1:8080/api/v1/health | grep startupWarning")
client.succeed("ping -c1 -W2 10.0.0.1")
router.reboot()
router.wait_for_unit("nixos-router-backend.service", timeout=180)
client.wait_until_succeeds("ping -c1 -W2 10.0.0.1", timeout=120)
# A real LAN address change returns a job before the old management path goes away.
api("POST", "/session", {"username":"admin", "password":"test-password-123"})
cfg["lan"]["ipv4Cidr"] = "192.168.60.1/24"
cfg["lan"]["dhcp"]["rangeStart"] = "192.168.60.100"
cfg["lan"]["dhcp"]["rangeEnd"] = "192.168.60.150"
api("PUT", "/config", cfg)
moved_job = api("POST", "/apply")["jobId"]
for node in [client, observer]:
    node.succeed("systemctl stop systemd-networkd; ip address flush dev eth1; rm -f /run/systemd/netif/leases/*; systemctl start systemd-networkd")
    node.wait_until_succeeds("ip -4 -o address show eth1 | grep '192.168.60.'", timeout=90)
    node.succeed("ping -c1 -W2 192.168.60.1")
    node.succeed("ping -c1 -W2 10.0.0.1")
base = "http://192.168.60.1:8080/api/v1"
api("POST", "/session", {"username":"admin", "password":"test-password-123"})
assert api("GET", "/jobs/" + moved_job)["status"] == "success"
client.succeed("dig +short @192.168.60.1 test.isp | grep -Fx 10.0.0.1")
router.fail("ip -4 addr show br-lan | grep 192.168.50.1")
print("MANAGED_NETWORK_BASELINE_PASS")
