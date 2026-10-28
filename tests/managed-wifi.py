import copy
import json
import shlex
import time

machine.start(allow_reboot=True)
machine.wait_for_unit("nixos-router-backend.service")
machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/healthz")
BASE = "http://127.0.0.1:8080/api/v1"
NS = "ip netns exec station "
COOKIE = "/root/wifi-cookie"


def api(method, path, body=None):
    command = f"curl --max-time 30 -fsS -b {COOKIE} -H 'Content-Type: application/json' -X {method}"
    if body is not None:
        command += " --data-binary " + shlex.quote(json.dumps(body))
    result = machine.succeed(command + " " + shlex.quote(BASE + path))
    return json.loads(result) if result.strip() else None


def login():
    payload = json.dumps({"username": "admin", "password": "synthetic-admin-password"})
    machine.succeed(
        f"curl --max-time 30 -fsS -c {COOKIE} -H 'Content-Type: application/json' "
        + "--data-binary " + shlex.quote(payload) + " " + BASE + "/session"
    )


def apply(configuration):
    api("PUT", "/config", configuration)
    job_id = api("POST", "/apply")["jobId"]
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        job = api("GET", "/jobs/" + job_id)
        if job["status"] in ("success", "failed"):
            assert job["status"] == "success", job
            return job
        time.sleep(1)
    raise AssertionError("managed WiFi apply did not finish")


def supplicant(password):
    configuration = (
        "ctrl_interface=/run/wifi-station-control\n"
        "network={\n"
        '    ssid="managed-wifi-test"\n'
        '    proto=RSN\n'
        '    key_mgmt=WPA-PSK\n'
        '    pairwise=CCMP\n'
        f'    psk="{password}"\n'
        "}\n"
    )
    machine.succeed("umask 077; printf %s " + shlex.quote(configuration) + " > /run/wifi-station.conf")
    # The driver's backdoor unit uses SIGHUP during shutdown. A daemon forked
    # from that shell would reload instead of stopping and delay reboot until
    # systemd's kill timeout. Keep the foreground client in its own unit/cgroup.
    machine.succeed(
        "systemd-run --unit=wifi-test-station --collect --service-type=exec "
        "--property=KillMode=control-group --property=KillSignal=SIGTERM "
        "--property=TimeoutStopSec=10s --property=After=wifi-test-links.service "
        "/run/current-system/sw/bin/ip netns exec station "
        "/run/current-system/sw/bin/wpa_supplicant -D nl80211 -i wlan1 "
        "-c /run/wifi-station.conf -f /run/wifi-station.log -dd"
    )
    machine.wait_until_succeeds("test -S /run/wifi-station-control/wlan1")


def station_status():
    # A tool/control-socket failure is a test failure, never proof of disconnection.
    raw = machine.succeed(NS + "wpa_cli -p /run/wifi-station-control -i wlan1 status")
    status = dict(line.split("=", 1) for line in raw.splitlines() if "=" in line)
    assert "wpa_state" in status, "No valid supplicant status received: " + raw
    return status


def wait_station_state(connected):
    status = {}
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        status = station_status()
        if (status["wpa_state"] == "COMPLETED") == connected:
            return status
        time.sleep(1)
    raise AssertionError(f"Supplicant did not reach connected={connected}: {status}")


def associated():
    status = wait_station_state(True)
    assert status.get("key_mgmt") == "WPA2-PSK", status
    assert status.get("pairwise_cipher") == "CCMP", status
    machine.succeed("ip -j link show wlan0 | jq -e '.[0].master == \"br-lan\"'")


def assert_no_lease():
    code, output = machine.execute(
        NS + "udhcpc -n -q -i wlan1 -t 2 -T 2 -s /etc/wifi-station-dhcp-hook 2>&1"
    )
    assert code == 1 and "broadcasting discover" in output and "no lease, failing" in output, (code, output)
    links = json.loads(machine.succeed(NS + "ip -j -4 addr show dev wlan1"))
    assert not any(address.get("scope") == "global" for link in links for address in link.get("addr_info", [])), links


def acquire_lease():
    machine.succeed(NS + "udhcpc -n -q -i wlan1 -t 8 -T 2 -s /etc/wifi-station-dhcp-hook")
    address = machine.succeed("cat /run/station-dhcp-address").strip()
    assert address in [f"192.168.77.{i}" for i in range(100, 111)], address
    machine.succeed(NS + "ping -c 2 -W 3 192.168.77.1")
    mac = machine.succeed(NS + "ip -j link show wlan1 | jq -r '.[0].address'").strip()
    # Lease evidence must come from the actual managed dnsmasq process.
    leases = machine.succeed("cat /var/lib/nixos-router/dnsmasq.leases")
    assert mac in leases and address in leases, leases
    machine.succeed(NS + "curl --max-time 10 -fsS http://192.168.77.1:8080/api/v1/healthz")


try:
    login()
    config = api("GET", "/config?redact=0")["config"]
    with subtest("WiFi is disabled until the authenticated API applies it"):
        assert config["wifi"]["enable"] is False
        machine.fail("systemctl is-active --quiet nixos-router-hostapd.service")
        machine.succeed("ip -j link show wlan0 | jq -e '.[0].master == null'")
        config["wifi"]["enable"] = True
        apply(config)
        machine.wait_for_unit("nixos-router-hostapd.service")
        machine.succeed("test $(stat -c %a /var/lib/nixos-router/managed/current/hostapd.conf) = 600")

    with subtest("Wrong WPA2 password fails authentication and DHCP"):
        supplicant("definitely-wrong-password")
        machine.wait_until_succeeds(
            "grep -E 'WRONG_KEY|pre-shared key may be incorrect' /run/wifi-station.log",
            timeout=120,
        )
        assert station_status()["wpa_state"] != "COMPLETED"
        assert_no_lease()
        machine.succeed("systemctl stop wifi-test-station.service")
        machine.succeed("test ! -S /run/wifi-station-control/wlan1")

    with subtest("Correct WPA2 password associates and obtains a real managed lease"):
        supplicant("synthetic-wifi-password")
        associated()
        acquire_lease()

    with subtest("API disabling WiFi stops AP and disconnects the station"):
        disabled = copy.deepcopy(config)
        disabled["wifi"]["enable"] = False
        apply(disabled)
        machine.fail("systemctl is-active --quiet nixos-router-hostapd.service")
        wait_station_state(False)
        machine.succeed(NS + "ip -4 addr flush dev wlan1")
        assert_no_lease()
        machine.succeed("ip -j link show wlan0 | jq -e '.[0].master == null'")

    with subtest("Re-enable and restart restore last-good AP despite disabled saved draft"):
        apply(config)
        associated()
        acquire_lease()
        api("PUT", "/config", disabled)  # Save only, deliberately do not apply.
        machine.succeed("systemctl restart nixos-router-backend.service")
        machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/healthz")
        associated()
        acquire_lease()
        assert api("GET", "/config")["config"]["wifi"]["enable"] is False

    with subtest("Full VM reboot restores applied AP and WPA2/DHCP software chain"):
        machine.reboot()
        machine.wait_for_unit("nixos-router-backend.service")
        # Type=simple marks routerd active before startup Restore finishes. The
        # HTTP listener is created only after Restore, so health is the gate.
        machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/healthz")
        machine.wait_for_unit("nixos-router-hostapd.service")
        login()
        assert api("GET", "/config")["config"]["wifi"]["enable"] is False
        supplicant("synthetic-wifi-password")
        associated()
        acquire_lease()
finally:
    machine.execute("journalctl -u nixos-router-backend -u nixos-router-hostapd -u nixos-router-dnsmasq -u wifi-test-station --no-pager > /root/managed-wifi-journal.log")
    machine.execute("iw dev > /root/managed-wifi-radio.log; ip netns exec station iw dev >> /root/managed-wifi-radio.log")
    machine.copy_from_machine("/root/managed-wifi-journal.log")
    machine.copy_from_machine("/root/managed-wifi-radio.log")
    machine.execute("test ! -e /run/wifi-station.log || cp /run/wifi-station.log /root/managed-wifi-station.log")
    if machine.execute("test -e /root/managed-wifi-station.log")[0] == 0:
        machine.copy_from_machine("/root/managed-wifi-station.log")
