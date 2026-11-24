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
MANAGEMENT_ALLOWED = not ROUTED
WIFI_PREFIX = "192.168.88" if ROUTED else "192.168.77"
WIFI_GATEWAY = WIFI_PREFIX + ".1"


def api(method, path, body=None):
    command = f"curl --max-time 30 -fsS -b {COOKIE} -H 'Content-Type: application/json' -X {method}"
    if method in ("PUT", "PATCH") or path == "/backup/restore":
        revision = api("GET", "/config")["config"]["revision"]
        command += " -H " + shlex.quote("If-Match: " + json.dumps(revision))
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


def apply(configuration, expect_success=True):
    api("PUT", "/config", configuration)
    job_id = api("POST", "/apply")["jobId"]
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        job = api("GET", "/jobs/" + job_id)
        if job["status"] in ("success", "failed"):
            assert job["status"] == ("success" if expect_success else "failed"), job
            payload = json.loads(job["payload"]) if isinstance(job["payload"], str) else job["payload"]
            if expect_success and payload.get("confirmation"):
                confirmation = api("POST", "/apply/confirm", {"id": payload["confirmation"]["id"]})
                assert confirmation["confirmation"]["state"] == "confirmed", confirmation
            return job
        time.sleep(1)
    raise AssertionError("managed WiFi apply did not finish")


def supplicant(password):
    credentials = ('    key_mgmt=SAE\n    ieee80211w=2\n    sae_password="' + password + '"\n') if SECURITY == "wpa3-sae" else ('    key_mgmt=WPA-PSK\n    psk="' + password + '"\n')
    configuration = (
        "ctrl_interface=/run/wifi-station-control\n"
        "network={\n"
        '    ssid="managed-wifi-test"\n'
        '    proto=RSN\n'
        '    pairwise=CCMP\n'
        + credentials +
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
    assert status.get("key_mgmt") == ("SAE" if SECURITY == "wpa3-sae" else "WPA2-PSK"), status
    assert status.get("pairwise_cipher") == "CCMP", status
    if len(RADIO_STANDARD) > 0:
        assert status.get("wifi_generation") == RADIO_STANDARD.removeprefix("wifi"), status
        machine.succeed("iw dev wlan0 info | grep -F " + shlex.quote("width: " + str(RADIO_WIDTH) + " MHz"))
    if ROUTED:
        machine.succeed("ip -j link show wlan0 | jq -e '.[0].master == null'")
    else:
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
    assert address in [f"{WIFI_PREFIX}.{i}" for i in range(100, 111)], address
    machine.succeed(NS + "ping -c 2 -W 3 " + WIFI_GATEWAY)
    mac = machine.succeed(NS + "ip -j link show wlan1 | jq -r '.[0].address'").strip()
    # Lease evidence must come from the actual managed dnsmasq process.
    leases = machine.succeed("cat /var/lib/nixos-router/dnsmasq.leases")
    assert mac in leases and address in leases, leases
    (machine.succeed if MANAGEMENT_ALLOWED else machine.fail)(NS + "curl --connect-timeout 2 --max-time 3 -fsS http://" + WIFI_GATEWAY + ":8080/api/v1/healthz")
    assert machine.succeed("cat /run/test-dhcp-wlan1-router").strip() == WIFI_GATEWAY
    assert machine.succeed("cat /run/test-dhcp-wlan1-dns").strip() == WIFI_GATEWAY


try:
    login()
    config = api("GET", "/config?redact=0")["config"]
    with subtest("WiFi is disabled until the authenticated API applies it"):
        assert config["wifi"]["enable"] is False
        machine.fail("systemctl is-active --quiet nixos-router-hostapd.service")
        machine.succeed("ip -j link show wlan0 | jq -e '.[0].master == null'")
        config["wifi"]["enable"] = True
        if len(RADIO_STANDARD) > 0:
            print(machine.succeed("iw phy phy0 info; iw phy phy0 channels; iw reg get"))
            caps = api("GET", "/capabilities/wifi")
            assert caps["source"] == "iw" and caps["available"], caps
            assert caps["radioBands"]["5g"]["he"], caps
            if RADIO_STANDARD == "wifi7":
                assert caps["radioBands"]["5g"]["eht"], caps
        apply(config)
        machine.wait_for_unit("nixos-router-hostapd.service")
        machine.succeed("test $(stat -c %a /var/lib/nixos-router/managed/current/hostapd.conf) = 600")

    with subtest(f"Wrong {SECURITY} password fails authentication and DHCP"):
        supplicant("definitely-wrong-password")
        if SECURITY == "wpa3-sae":
            machine.wait_until_succeeds("grep -q 'SAE:' /run/wifi-station.log", timeout=120)
            for _ in range(15):
                assert station_status()["wpa_state"] != "COMPLETED"
                time.sleep(1)
        else:
            machine.wait_until_succeeds(
                "grep -E 'WRONG_KEY|pre-shared key may be incorrect' /run/wifi-station.log",
                timeout=120,
            )
        assert station_status()["wpa_state"] != "COMPLETED"
        assert_no_lease()
        machine.succeed("systemctl stop wifi-test-station.service")
        machine.succeed("test ! -S /run/wifi-station-control/wlan1")

    with subtest(f"Correct {SECURITY} password associates and obtains a real managed lease"):
        supplicant("synthetic-wifi-password")
        associated()
        acquire_lease()

    if ROUTED:
        with subtest("Wired and WiFi obtain separate DHCP options and real DNS/NAT"):
            machine.wait_for_unit("wifi-test-isp-dns.service")
            machine.wait_for_unit("wifi-test-isp-http.service")
            machine.succeed("ip netns exec wired udhcpc -n -q -i wired0 -t 8 -T 2 -s /etc/wifi-station-dhcp-hook")
            wired_address = machine.succeed("cat /run/test-dhcp-wired0-address").strip()
            station_address = machine.succeed("cat /run/test-dhcp-wlan1-address").strip()
            assert wired_address in [f"192.168.77.{i}" for i in range(100,111)], wired_address
            assert machine.succeed("cat /run/test-dhcp-wired0-router").strip() == "192.168.77.1"
            assert machine.succeed("cat /run/test-dhcp-wired0-dns").strip() == "192.168.77.1"
            for ns,gateway in (("station",WIFI_GATEWAY),("wired","192.168.77.1")):
                prefix = "ip netns exec " + ns + " "
                machine.succeed(prefix + "dig +short @" + gateway + " uplink.test A | grep -Fx 203.0.113.1")
                observed = machine.succeed(prefix + "curl --noproxy '*' -fsS --max-time 5 http://203.0.113.1:8090/").strip()
                assert observed == "10.99.0.2", (ns,observed)
            machine.fail(NS + "ping -c1 -W2 " + wired_address)
            machine.fail("ip netns exec wired ping -c1 -W2 " + station_address)
            machine.fail("ip netns exec isp ping -c1 -W2 " + station_address)
            machine.fail(NS + "curl --connect-timeout 2 --max-time 3 -fsS http://192.168.77.1:8080/api/v1/healthz")
        with subtest("Changing the AP channel preserves the wired gateway generation"):
            dns_invocation = "systemctl show -p InvocationID --value nixos-router-dnsmasq.service"
            generation = "readlink /var/lib/nixos-router/managed/current"
            before = machine.succeed(dns_invocation + "; " + generation)
            ap = config["wifi"]["aps"][0]
            channel = 153 if ap["band"] == "5g" else 11
            ap["channel"] = channel
            apply(config)
            assert machine.succeed(dns_invocation + "; " + generation) == before
            machine.succeed(f"iw dev wlan0 info | grep -F 'channel {channel} ('")
            assert machine.succeed("ip netns exec wired curl --noproxy '*' -fsS --max-time 5 http://203.0.113.1:8090/").strip() == "10.99.0.2"
            associated()
            acquire_lease()
        with subtest("Explicit cross-subnet and management permissions apply and revoke"):
            config["wifi"]["network"]["allowManagement"] = True
            config["wifi"]["network"]["allowWiredAccess"] = True
            apply(config)
            MANAGEMENT_ALLOWED = True
            associated()
            acquire_lease()
            machine.succeed(NS + "ping -c1 -W2 " + wired_address)
            machine.succeed("ip netns exec wired ping -c1 -W2 " + station_address)
            config["wifi"]["network"]["allowManagement"] = False
            config["wifi"]["network"]["allowWiredAccess"] = False
            apply(config)
            MANAGEMENT_ALLOWED = False
            associated()
            acquire_lease()
            # These flows already existed: revocation must precede conntrack accept.
            machine.fail(NS + "ping -c1 -W2 " + wired_address)
            machine.fail("ip netns exec wired ping -c1 -W2 " + station_address)

    if ROUTED:
        with subtest("Independent zone restrictions survive optional WAN filter disable"):
            config["firewall"]["enable"] = False
            apply(config)
            associated()
            acquire_lease()
            machine.fail(NS + "ping -c1 -W2 " + wired_address)
            machine.fail("ip netns exec wired ping -c1 -W2 " + station_address)
            machine.fail("ip netns exec isp ping -c1 -W2 " + station_address)
            assert machine.succeed(NS + "curl --noproxy '*' -fsS --max-time 5 http://203.0.113.1:8090/").strip() == "10.99.0.2"
            config["firewall"]["enable"] = True
            apply(config)
            associated()
            acquire_lease()
        with subtest("Failed AP candidate restores previous independent address and traffic"):
            failed = copy.deepcopy(config)
            failed["wifi"]["network"]["ipv4Cidr"] = "192.168.89.1/24"
            failed["wifi"]["network"]["dhcp"]["rangeStart"] = "192.168.89.100"
            failed["wifi"]["network"]["dhcp"]["rangeEnd"] = "192.168.89.110"
            machine.succeed("touch /run/test-ap-fail-once")
            apply(failed, expect_success=False)
            machine.succeed("ip -4 -o address show wlan0 | grep '192.168.88.1/24'")
            machine.fail("ip -4 -o address show wlan0 | grep '192.168.89.1/24'")
            associated()
            acquire_lease()
            assert machine.succeed(NS + "curl --noproxy '*' -fsS --max-time 5 http://203.0.113.1:8090/").strip() == "10.99.0.2"

    with subtest("API disabling WiFi stops AP and disconnects the station"):
        disabled = copy.deepcopy(config)
        disabled["wifi"]["enable"] = False
        apply(disabled)
        machine.fail("systemctl is-active --quiet nixos-router-hostapd.service")
        if ROUTED:
            machine.succeed("test -z \"$(ip -4 -o address show wlan0 scope global)\"")
            machine.succeed("test ! -e /var/lib/nixos-router/managed/wifi-ipv4.json")
        wait_station_state(False)
        machine.succeed(NS + "ip -4 addr flush dev wlan1")
        assert_no_lease()
        machine.succeed("ip -j link show wlan0 | jq -e '.[0].master == null'")

    with subtest("Re-enable and restart restore last-good AP despite disabled saved draft"):
        apply(config)
        associated()
        acquire_lease()
        api("PUT", "/config", disabled)  # Save only, deliberately do not apply.
        machine.succeed("systemctl restart nixos-router-executor.service")
        machine.wait_for_unit("nixos-router-executor.service")
        machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/healthz")
        associated()
        acquire_lease()
        assert api("GET", "/config")["config"]["wifi"]["enable"] is False

    with subtest(f"Full VM reboot restores applied AP and {SECURITY}/DHCP software chain"):
        machine.reboot()
        machine.wait_for_unit("nixos-router-backend.service")
        # The executor notifies readiness after Restore; HTTP availability also
        # proves the independent API has connected to that restored runtime.
        machine.wait_until_succeeds("curl -fsS http://127.0.0.1:8080/api/v1/healthz")
        machine.wait_for_unit("nixos-router-hostapd.service")
        login()
        assert api("GET", "/config")["config"]["wifi"]["enable"] is False
        supplicant("synthetic-wifi-password")
        associated()
        acquire_lease()
    if ROUTED:
        with subtest("Switching routed WiFi to bridge and back withdraws only its gateway"):
            bridged = copy.deepcopy(config)
            bridged["wifi"]["bridgeToLan"] = True
            apply(bridged)
            wait_station_state(True)
            machine.succeed("ip -j link show wlan0 | jq -e '.[0].master == \"br-lan\"'")
            machine.succeed("test -z \"$(ip -4 -o address show wlan0 scope global)\"")
            machine.succeed(NS + "udhcpc -n -q -i wlan1 -t 8 -T 2 -s /etc/wifi-station-dhcp-hook")
            address = machine.succeed("cat /run/test-dhcp-wlan1-address").strip()
            assert address in [f"192.168.77.{i}" for i in range(100,111)], address
            machine.succeed(NS + "curl --max-time 5 -fsS http://192.168.77.1:8080/api/v1/healthz")
            apply(config)
            associated()
            acquire_lease()
finally:
    machine.execute("journalctl -u nixos-router-backend -u nixos-router-executor -u nixos-router-hostapd -u nixos-router-dnsmasq -u wifi-test-station --no-pager > /root/managed-wifi-journal.log")
    machine.execute("iw dev > /root/managed-wifi-radio.log; ip netns exec station iw dev >> /root/managed-wifi-radio.log")
    machine.copy_from_machine("/root/managed-wifi-journal.log")
    machine.copy_from_machine("/root/managed-wifi-radio.log")
    machine.execute("test ! -e /run/wifi-station.log || cp /run/wifi-station.log /root/managed-wifi-station.log")
    if machine.execute("test -e /root/managed-wifi-station.log")[0] == 0:
        machine.copy_from_machine("/root/managed-wifi-station.log")
