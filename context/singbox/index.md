<!-- ledgerloop:generated — safe to regenerate; do not hand-edit -->
# Singbox

- [invariants](./invariants.md)
- [log](./log.md)

The sandbox keeps independent direct DO and DO + WARP ordinary VLESS tunnels
([record #318](../../tasks/done/318-do-warp-egress.md)), with WARP in a separate DO
service. [Direct client profiles #320](../../tasks/done/320-direct-do-clients.md)
add one independent user per DO listener and public IPv4 TCP443/TCP2443 access;
existing RAC users and routing remain. Earlier topology/access records remain
historical. These operational changes add no application code or routing contract.
