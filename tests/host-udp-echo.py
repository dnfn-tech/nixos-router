"""VM-only host-service fixture; firewall policy decides which WAN port passes."""
import selectors
import socket

selector = selectors.DefaultSelector()
for family, address in ((socket.AF_INET, "0.0.0.0"), (socket.AF_INET6, "::")):
    for port in (9993, 9994):
        listener = socket.socket(family, socket.SOCK_DGRAM)
        if family == socket.AF_INET6:
            listener.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
        listener.bind((address, port))
        selector.register(listener, selectors.EVENT_READ)

while True:
    for key, _ in selector.select():
        payload, peer = key.fileobj.recvfrom(4096)
        key.fileobj.sendto(payload, peer)
