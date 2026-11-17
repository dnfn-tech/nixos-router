import json
import shlex
import time

start_all()
machine.wait_for_unit("nixos-router-executor.service", timeout=240)
machine.wait_for_unit("nixos-router-backend.service", timeout=90)
machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/health", timeout=90)
base = "http://127.0.0.1:8080/api/v1"


def api(method, path, body=None):
    command = "curl -fsS --max-time 10 -b /run/legacy-cookie -c /run/legacy-cookie -X " + method
    if method in ("PUT", "PATCH") or path == "/backup/restore":
        revision = api("GET", "/config")["config"]["revision"]
        command += " -H " + shlex.quote("If-Match: " + json.dumps(revision))
    if body is not None:
        command += " -H 'Content-Type: application/json' --data " + shlex.quote(json.dumps(body))
    return json.loads(machine.succeed(command + " " + shlex.quote(base + path)))


def pid(unit):
    return machine.succeed("systemctl show -p MainPID --value " + unit).strip()


def await_job(jid, expected):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        code, raw = machine.execute("curl -fsS --max-time 5 -b /run/legacy-cookie " + base + "/jobs/" + jid)
        if code == 0:
            job = json.loads(raw)
            if job.get("status") in ("success", "failed"):
                assert job["status"] == expected, job
                return job
        time.sleep(1)
    raise Exception("executor job did not settle: " + jid)


with subtest("Existing account, session and draft survive real UID migration"):
    original = api("GET", "/config")["config"]  # Session created before migration.
    assert original["system"]["hostname"] == "legacy-draft"
    legacy_job = api("GET", "/jobs/legacy-running")
    assert legacy_job["status"] == "failed", legacy_job
    assert "interrupted" in json.dumps(legacy_job), legacy_job
    applied = json.loads(machine.succeed("cat /var/lib/nixos-router/managed/last-good.json"))
    assert applied["system"]["hostname"] == "router"
    api("POST", "/session", {"username": "admin", "password": "existing-test-password"})
    uid = machine.succeed("id -u nixos-router-api").strip()
    assert uid != "0"
    api_pid = pid("nixos-router-backend")
    executor_pid = pid("nixos-router-executor")
    machine.succeed("grep -E '^Uid:[[:space:]]+" + uid + "[[:space:]]' /proc/" + api_pid + "/status")
    machine.succeed("grep -E '^CapEff:[[:space:]]+0+$' /proc/" + api_pid + "/status")
    machine.succeed("grep -E '^Uid:[[:space:]]+0[[:space:]]' /proc/" + executor_pid + "/status")
    machine.fail("runuser -u nixos-router-api -- cat /var/lib/nixos-router/managed/last-good.json")
    machine.fail("runuser -u nixos-router-api -- touch /var/lib/nixos-router/forged")
    machine.fail("runuser -u nixos-router-api -- ip link add forbidden-link type dummy")
    machine.succeed("test $(stat -c %a /var/lib/nixos-router-api) = 700")
    machine.succeed("test $(stat -c %U /var/lib/nixos-router-api/config.json) = nixos-router-api")

with subtest("Peer authentication and fixed RPC reject root peer and arbitrary commands"):
    socket = "/run/nixos-router-executor/control.sock"
    machine.fail("curl -fsS --max-time 3 --unix-socket " + socket + " http://executor/v1/applied")
    rpc = "runuser -u nixos-router-api -- curl -sS --max-time 3 --unix-socket " + socket
    assert machine.succeed(rpc + " -o /dev/null -w '%{http_code}' -X POST --data '{\"command\":\"touch /tmp/rpc-injected\"}' http://executor/v1/exec").strip() == "404"
    assert machine.succeed(rpc + " -o /dev/null -w '%{http_code}' 'http://executor/v1/leases?path=/etc/shadow'").strip() == "400"
    machine.fail("test -e /tmp/rpc-injected")
    assert api("GET", "/clients")["source"] == "leases"

with subtest("API crash leaves accepted network transaction running under its original ID"):
    machine.succeed("mkdir -p /run/systemd/system/nixos-router-dnsmasq.service.d")
    pause = "[Service]\nExecStartPre=/bin/sh -c 'if test -e /run/pause-request; then rm /run/pause-request; touch /run/apply-paused; while test -e /run/hold-apply; do sleep 0.1; done; fi'\n"
    machine.succeed("printf %s " + shlex.quote(pause) + " > /run/systemd/system/nixos-router-dnsmasq.service.d/pause.conf; systemctl daemon-reload; touch /run/pause-request /run/hold-apply")
    jid = api("POST", "/apply")["jobId"]
    machine.wait_until_succeeds("test -e /run/apply-paused")
    executor_pid = pid("nixos-router-executor")
    old_api_pid = pid("nixos-router-backend")
    machine.succeed("systemctl kill --kill-whom=main --signal=SIGKILL nixos-router-backend")
    machine.wait_until_succeeds("test $(systemctl show -p MainPID --value nixos-router-backend) != " + old_api_pid)
    machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/health", timeout=90)
    assert pid("nixos-router-executor") == executor_pid
    machine.succeed("rm /run/hold-apply")
    await_job(jid, "success")
    machine.succeed("rm /run/systemd/system/nixos-router-dnsmasq.service.d/pause.conf; systemctl daemon-reload")

with subtest("Re-running preparation and restarting API never restores old draft"):
    draft = json.loads(json.dumps(original))
    draft["system"]["hostname"] = "saved-new-draft"
    api("PUT", "/config", draft)
    print(machine.succeed("stat -c '%n %a %U' /var/lib/nixos-router /var/lib/nixos-router/dnsmasq.leases /var/lib/nixos-router/managed/dnsmasq.pid"))
    machine.succeed("systemctl restart nixos-router-state-init; systemctl restart nixos-router-backend")
    machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/health", timeout=90)
    assert api("GET", "/config")["config"]["system"]["hostname"] == "saved-new-draft"

with subtest("Corrupt executor last-good prevents readiness and API startup"):
    machine.succeed("systemctl stop nixos-router-backend nixos-router-executor")
    machine.succeed("cp /var/lib/nixos-router/managed/last-good.json /var/lib/nixos-router/last-good.backup; printf broken > /var/lib/nixos-router/managed/last-good.json")
    machine.fail("systemctl start nixos-router-executor")
    machine.succeed("systemctl start nixos-router-backend")
    machine.fail("curl -fsS --max-time 3 http://127.0.0.1:8080/api/v1/health")
    machine.succeed("systemctl stop nixos-router-backend nixos-router-executor; mv /var/lib/nixos-router/last-good.backup /var/lib/nixos-router/managed/last-good.json; systemctl reset-failed; systemctl start nixos-router-executor nixos-router-backend")
    machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/health", timeout=90)
    assert api("GET", "/config")["config"]["system"]["hostname"] == "saved-new-draft"
