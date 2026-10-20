# Local WireGuard HTTP smoke check

`wireproxy-smoke.py` is a standalone manual tool, independent of the daemon and
its test suite. It needs Python 3 (standard library only), an already installed
[wireproxy](https://github.com/windtf/wireproxy), and curl. It installs nothing.

```sh
python3 tools/wireproxy-smoke.py ./client.conf https://example.com/ --repeat 2 --timeout 30
```

Supply an existing WireGuard configuration and every URL explicitly. Optional
`--wireproxy /path/to/wireproxy` selects the executable; `--startup-timeout 10`
bounds local listener startup. Run only when contacting the profile's VPN endpoint
and the supplied URLs is intended. The example URL is illustrative, never a default.

The script copies the profile byte for byte into a private temporary directory
and imports it with `WGConfig`; its DNS and MTU remain as provided. It starts one
foreground wireproxy child with a loopback SOCKS listener, then makes sequential
curl requests using `socks5h`. No system VPN, routes, interfaces, or source profile
are changed. curl configuration files and inherited `NO_PROXY` cannot bypass the
explicit proxy; TLS verification remains enabled and redirects are not followed.
Temporary files are removed and only the owned child is stopped on completion,
error, or interruption. Child logs are discarded because configuration errors
might expose keys. Startup errors therefore require checking the profile and
wireproxy version separately.

Each request prints its round/index, HTTP status, total time, time to first byte,
download bytes/rate, and curl exit status. URLs are identified by their argument
order. HTTP 200–399 is accepted; HTTP errors, missing status, curl failures, startup
failure, or child exit produce a nonzero overall exit status. Requests have a
30-second default timeout; shutdown has bounded terminate/kill waits. Repeats are
limited to 100 and timeouts to 3600 seconds. Use `--help` for options.

A ready local listener does not establish a WireGuard connection. Only request
results establish application success. The provided peer and existing server rules
determine routing: the script cannot select Anton Direct/WRAP or prove the chosen
route from HTTP 200 alone. These are end-to-end TCP/HTTP(S) observations through
wireproxy, including remote hostname resolution when needed. They do not isolate
DNS or tunnel transport latency and do not prove complete UDP/QUIC behavior, PMTU,
or physical NIC behavior. The private copy/import approach intentionally leaves
product runtime invariants unchanged; this is not a daemon feature or a replacement
for integration tests.
