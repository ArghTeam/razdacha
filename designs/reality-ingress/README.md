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


## Authorized static staging delivery

The user accepted this preview and authorized static delivery at `/preview/reality/`
on the designated staging gateway. Static delivery and public HTTPS/browser
postflight passed on 2026-11-04. The served HTML matched the reviewed source; the
main panel, API and existing application state remained unchanged. See the
[delivery record](../../tasks/done/300-ingress-ui-preview.md#static-staging-acceptance)
and [issue #300](https://github.com/ArghTeam/razdacha/issues/300) for scope and limits.

1. Use the existing HTTPS nginx server and a dedicated static-page location. Keep
   the main panel and API routes unchanged; serve only the synthetic preview file.
2. Retain a fresh private copy and identity of the original nginx configuration,
   and verify the uploaded page against the reviewed source hash.
3. Run `nginx -t` before a graceful nginx reload. On validation or reload failure,
   restore the original configuration and verify it before retrying.
4. Check the preview response/content, existing panel/API behavior and service
   health. Record redacted results in the task issue after deployment.

This delivery changes no application binary, application configuration, client
policy, network rules, WireGuard service or REALITY listener. Production is outside
the authorized deployment scope. Keep operational connection details and raw
configuration backups private.
