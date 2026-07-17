# krun branch status

The plugin control API runs in the krun guest and reaches SPR through:

```text
host Unix socket -> vsock 4040 -> guest Unix socket
```

Periodic router-state polling works through the guest's SPR-managed TAP.
Two host-integration features are not equivalent yet:

- the SPRBus subscription still names the host Unix socket and therefore
  falls back to periodic polling inside the guest;
- Wake-on-LAN and multicast discovery need a small host agent because routed
  TAP networking does not place the guest directly on every LAN broadcast
  domain.

The branch keeps these limitations explicit rather than exposing SPRBus or a
debug/control API over TCP.
