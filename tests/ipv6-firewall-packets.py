"""Packet verdict checks; invoked only inside the Go test's disposable netns."""
import json
import os
import socket
import struct
import subprocess
import sys

assert os.environ.get('ROUTERD_TEST_NFT_PARENT_NS')
assert os.readlink('/proc/self/ns/net') != os.environ['ROUTERD_TEST_NFT_PARENT_NS'], 'refusing host networking'
mode, iface = sys.argv[1:]

def ip(*args):
    subprocess.run(['ip', *args], check=True, stdout=subprocess.DEVNULL)

def checksum(data):
    if len(data) % 2:
        data += b'\0'
    value = sum(struct.unpack('!' + 'H' * (len(data) // 2), data))
    while value >> 16:
        value = (value & 65535) + (value >> 16)
    return (~value) & 65535

ip('link', 'add', iface, 'type', 'veth', 'peer', 'name', 'probe0')
try:
    for name in [iface, 'probe0']:
        ip('link', 'set', name, 'addrgenmode', 'none')
        subprocess.run(['sysctl', '-qw', f'net/ipv6/conf/{name}/accept_ra=0', f'net/ipv6/conf/{name}/autoconf=0'], check=True)
        ip('link', 'set', name, 'up')
    ip('-6', 'address', 'add', 'fe80::1/64', 'dev', iface, 'nodad')
    ip('-4', 'address', 'add', '192.0.2.1/24', 'dev', iface)
    dst = socket.inet_pton(socket.AF_INET6, 'fe80::1')
    def mac(name):
        links = json.loads(subprocess.check_output(['ip', '-j', 'link', 'show', 'dev', name], text=True))
        return bytes.fromhex(links[0]['address'].replace(':', ''))
    ethernet = mac(iface) + mac('probe0') + b'\x86\xdd'
    with socket.socket(socket.AF_PACKET, socket.SOCK_RAW) as sender:
        sender.bind(('probe0', 0))
        def probe(protocol, payload, offset, receiver, allowed, hop=255, source='fe80::2'):
            src = socket.inet_pton(socket.AF_INET6, source)
            pseudo = src + dst + struct.pack('!I3xB', len(payload), protocol)
            value = checksum(pseudo + payload) or 65535
            payload = payload[:offset] + struct.pack('!H', value) + payload[offset + 2:]
            packet = struct.pack('!IHBB16s16s', 6 << 28, len(payload), protocol, hop, src, dst) + payload
            sender.send(ethernet + packet)
            receiver.settimeout(0.2)
            try:
                data = receiver.recv(4096)
                received = data == (payload[8:] if protocol == 17 else payload)
            except socket.timeout:
                received = False
            assert received == allowed, (mode, protocol, hop, source, allowed, received)
        with socket.socket(socket.AF_INET6, socket.SOCK_RAW, socket.IPPROTO_TCP) as tcp:
            tcp.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, iface.encode() + b'\0')
            for port in [22, 8080, 8081]:
                syn = struct.pack('!HHIIBBHHH', 40000, port, 1, 0, 5 << 4, 2, 65535, 0, 0)
                probe(6, syn, 16, tcp, port == 8081 and mode == 'unfiltered')
        if mode != 'unfiltered':
            with socket.socket(socket.AF_INET6, socket.SOCK_RAW, socket.IPPROTO_ICMPV6) as icmp:
                icmp.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, iface.encode() + b'\0')
                ra = struct.pack('!BBHBBHII', 134, 0, 0, 64, 0, 30, 0, 0)
                probe(58, ra, 2, icmp, mode != 'disabled')
                probe(58, ra, 2, icmp, False, hop=254)
                probe(58, ra, 2, icmp, False, source='2001:db8::2')
                ns = struct.pack('!BBHI', 135, 0, 0, 0) + dst
                probe(58, ns, 2, icmp, mode != 'disabled')
        for port, allowed in [(546, mode in ['dhcpv6', 'pppoe6', 'pppoe-services', 'unfiltered']), (548, mode == 'unfiltered'), (9993, mode in ['host-service', 'pppoe-services', 'unfiltered']), (9994, mode == 'unfiltered')]:
            with socket.socket(socket.AF_INET6, socket.SOCK_DGRAM) as udp:
                udp.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, iface.encode() + b'\0')
                udp.bind(('::', port))
                payload = b'router-firewall-probe'
                datagram = struct.pack('!HHHH', 547, port, 8 + len(payload), 0) + payload
                probe(17, datagram, 6, udp, allowed)
        # Exercise IPv4 as well: the host policy must cover both families.
        for port in [9993, 9994]:
            allowed = mode == 'unfiltered' or (port == 9993 and mode in ['host-service', 'pppoe-services'])
            src4, dst4 = socket.inet_aton('192.0.2.2'), socket.inet_aton('192.0.2.1')
            payload = b'router-ipv4-host-service'
            udp4 = struct.pack('!HHHH', 45000, port, 8 + len(payload), 0) + payload
            pseudo = src4 + dst4 + struct.pack('!BBH', 0, 17, len(udp4))
            udp4 = udp4[:6] + struct.pack('!H', checksum(pseudo + udp4) or 65535) + udp4[8:]
            header = struct.pack('!BBHHHBBH4s4s', 0x45, 0, 20 + len(udp4), 100, 0, 64, 17, 0, src4, dst4)
            header = header[:10] + struct.pack('!H', checksum(header)) + header[12:]
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
                udp.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, iface.encode() + b'\0')
                udp.bind(('0.0.0.0', port)); udp.settimeout(0.2)
                sender.send(mac(iface) + mac('probe0') + b'\x08\x00' + header + udp4)
                try: received = udp.recv(4096) == payload
                except socket.timeout: received = False
                assert received == allowed, (mode, 'IPv4', port, allowed, received)

finally:
    ip('link', 'delete', iface)
