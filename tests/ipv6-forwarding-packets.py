"""Real routed IPv6 packets; only called inside the Go test's isolated netns."""
import json, os, socket, struct, subprocess, sys, time
assert os.environ.get('ROUTERD_TEST_FORWARD_PARENT_NS')
assert os.readlink('/proc/self/ns/net') != os.environ['ROUTERD_TEST_FORWARD_PARENT_NS']
def run(*args): subprocess.run(args,check=True,stdout=subprocess.DEVNULL)
def setting(iface,name,value): run('sysctl','-qw',f'net/ipv6/conf/{iface}/{name}={value}')
def mac(iface): return bytes.fromhex(json.loads(subprocess.check_output(['ip','-j','link','show','dev',iface]))[0]['address'].replace(':',''))
def checksum(data):
    if len(data)%2:data+=b'\0'
    s=sum(struct.unpack('!'+'H'*(len(data)//2),data))
    while s>>16:s=(s&65535)+(s>>16)
    return (~s)&65535
for iface,peer,net in [('br-lan','client0',1),('wan0','server0',2)]:
    member = 'lan0' if iface == 'br-lan' else iface
    run('ip','link','add',member,'type','veth','peer','name',peer)
    if iface == 'br-lan':
        run('ip','link','add',iface,'type','bridge')
        run('ip','link','set',member,'master',iface)
        setting(member,'accept_ra',0);setting(member,'autoconf',0)
        run('ip','link','set',member,'up')
    for name in [iface,peer]:
        setting(name,'accept_ra',0);setting(name,'autoconf',0)
        run('ip','link','set',name,'up')
    run('ip','-6','addr','add',f'2001:db8:{net}::1/64','dev',iface,'nodad')
    run('ip','-6','neigh','add',f'2001:db8:{net}::2','lladdr',mac(peer).hex(':'),'nud','permanent','dev',iface)
def forward(reverse=False, unsolicited=False):
    src=socket.inet_pton(socket.AF_INET6,'2001:db8:2::2' if reverse else '2001:db8:1::2')
    dst=socket.inet_pton(socket.AF_INET6,'2001:db8:1::2' if reverse else '2001:db8:2::2')
    sport,dport=(9000,40000) if reverse else (40000,9000)
    if unsolicited:sport=9001
    udp=struct.pack('!HHHH',sport,dport,12,0)+b'test'
    c=checksum(src+dst+struct.pack('!I3xB',len(udp),17)+udp) or 65535
    udp=udp[:6]+struct.pack('!H',c)+udp[8:]
    incoming,peer,outgoing=('wan0','server0','client0') if reverse else ('br-lan','client0','server0')
    frame=mac(incoming)+mac(peer)+b'\x86\xdd'+struct.pack('!IHBB16s16s',6<<28,len(udp),17,64,src,dst)+udp
    with socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(0x86dd)) as receiver, socket.socket(socket.AF_PACKET,socket.SOCK_RAW) as sender:
        receiver.bind((outgoing,0));sender.bind((peer,0));receiver.settimeout(.3);sender.send(frame)
        end=time.monotonic()+.3
        while time.monotonic()<end:
            try: packet=receiver.recv(4096)
            except socket.timeout:return False
            if packet[22:38]==src and packet[38:54]==dst and packet.endswith(b'test'):
                assert packet[21]==63,packet.hex()  # Routed once, addresses unchanged: no NAT66.
                return True
    return False

def policy(action):
    env=dict(os.environ,ROUTERD_TEST_FORWARD_ACTION=action)
    subprocess.run([sys.argv[1],'-test.run=^TestIPv6ForwardingKernel$'],env=env,check=True)

run('ip','link','add','external0','type','dummy')
external=open('/proc/sys/net/ipv6/conf/external0/force_forwarding').read()
assert open('/proc/sys/net/ipv6/conf/all/forwarding').read().strip()=='0'
policy('disable')
assert not forward(), 'unexpected initial transit'
policy('enable')
assert forward(), 'LAN to WAN not forwarded'
assert forward(reverse=True), 'established WAN return not forwarded'
assert not forward(reverse=True,unsolicited=True), 'unsolicited WAN transit allowed'
# Exercise actual IPv6 Packet Too Big generation with a smaller WAN IPv6
# MTU. The Ethernet/IPv4 link MTU remains 1500.
setting('wan0','mtu',1400)
assert json.loads(subprocess.check_output(['ip','-j','link','show','dev','wan0']))[0]['mtu']==1500
src=socket.inet_pton(socket.AF_INET6,'2001:db8:1::2')
dst=socket.inet_pton(socket.AF_INET6,'2001:db8:2::2')
payload=b'pmtu-probe'*140
udp=struct.pack('!HHHH',40001,9000,8+len(payload),0)+payload
c=checksum(src+dst+struct.pack('!I3xB',len(udp),17)+udp) or 65535
udp=udp[:6]+struct.pack('!H',c)+udp[8:]
frame=mac('br-lan')+mac('client0')+b'\x86\xdd'+struct.pack('!IHBB16s16s',6<<28,len(udp),17,64,src,dst)+udp
with socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(0x86dd)) as receiver, socket.socket(socket.AF_PACKET,socket.SOCK_RAW) as sender:
    receiver.bind(('client0',0));receiver.settimeout(2);sender.bind(('client0',0));sender.send(frame)
    end=time.monotonic()+2
    found=False
    while time.monotonic()<end:
        try: packet=receiver.recv(4096)
        except socket.timeout: break
        if len(packet)>=62 and packet[20]==58 and packet[54]==2 and packet[38:54]==src:
            assert struct.unpack('!I',packet[58:62])[0]==1400,packet.hex()
            assert packet[62:][8:24]==src and packet[62:][24:40]==dst,packet.hex()
            found=True;break
    assert found, 'oversized LAN packet did not receive IPv6 Packet Too Big with WAN MTU'
assert forward(), 'small IPv6 traffic failed after MTU reduction'
setting('wan0','mtu',1500)
policy('disable')
assert not forward(), 'LAN transit remains after disable'
assert not forward(reverse=True), 'return transit remains after disable'
assert open('/proc/sys/net/ipv6/conf/all/forwarding').read().strip()=='0'
assert open('/proc/sys/net/ipv6/conf/external0/force_forwarding').read()==external
print('IPv6 transit, return, unsolicited rejection, disable and foreign isolation passed')
