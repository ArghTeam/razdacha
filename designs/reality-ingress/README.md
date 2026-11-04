# Connection interface preview

Open `index.html` directly in a browser. The preview is self-contained and works
without a backend, build step or network connection. Alternatively, from this
directory run `python3 -m http.server 8765 --bind 127.0.0.1` and open
`http://127.0.0.1:8765/`.

The initial view opens Laptop details. Close it to inspect the synthetic client
inventory, use each row's menu, open Network settings, or change the preview theme.
WireGuard and Reality are independent choices for a client. Profile delivery,
Save, Apply and key-management confirmations simulate the proposed interaction
locally. Reloading the page resets every example.

All profile text is intentionally nonfunctional. Copy/download transfers only
synthetic example text. There are no API requests, server actions, real credentials
or QR codes. Clipboard content and downloaded files remain outside the page.

This is a proposed interface for [issue #300](https://github.com/ArghTeam/razdacha/issues/300)
and the parallel-ingress work in [issue #297](https://github.com/ArghTeam/razdacha/issues/297).
It does not implement the product transport, authentication, policy generation,
revocation, recovery or DPI resistance. Current embedded UI/runtime invariants
are unchanged. The example TCP port 8443 is not a finalized default.
