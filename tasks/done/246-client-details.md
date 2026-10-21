# Unified client details

Issue: #246. Parent: #242.

## Intended behavior

Each inventory row exposes one Details action. A modal groups identity/enabled state, explicit shared/whole-Internet routing and a WireGuard configuration disclosure. Name, enabled and exit changes use one transactional PATCH containing only changed fields. Save does not replace the editor or a newer draft. Close, Escape and backdrop all discard unsaved edits; the footer says so explicitly. No partial discard-confirmation flow is introduced.

Creation remains single flight and opens the same editor with configuration expanded. Server-generated text, filename, QR and download URLs remain modal-owned. Copy captures current configuration and guards asynchronous feedback; copied clipboard content is intentionally external. Saved rename invalidates and reloads open configuration. Errors/retry never erase other drafts. Missing/disabled/empty-pool exits remain selected and blocked without fallback.

Deletion uses a separate inline danger confirmation with affected/orphaned rule names and explicit reassignment, activation and Apply guidance. Cancellation preserves the current editor. Enable/disable and deletion may affect WireGuard access during the daemon's 10-second synchronization before Apply; routing/rule publication still requires Apply (ADR 0026/0028).

All asynchronous callbacks verify their session; interaction rendering additionally verifies modal lifetime. Completed same-session mutations update inventory/pending after ordinary close, but cannot overwrite another dialog or new draft. Only submitted fields update local inventory after PATCH.

## Checkout and ownership

Primary: `/Users/romanzhigalkin/Web/razdacha`, `main`. Remote: `origin`, `git@github.com:ArghTeam/razdacha.git`; target: `main`. Root fetched base `3a7aaf976ff7a32544a6d5a53a2d4fad8b65f6ff` and created `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/feat/246-client-details`, branch `feat/246-client-details`.

Own peers.js, client-scoped app.css, Client sections in UI screen documentation, UI invariants, this specification and client browser tests. No backend, shared shell/auth protocol, staging/deployment, other repositories or framework/build changes.

## Acceptance

Synthetic Chromium coverage includes creation single-flight, save/error/retry, changed-field payloads and replacement drafts, routing missing/disabled/empty/custom selections, configuration loading/error/retry/rename invalidation, deletion preflight/effects/cancel, old-session success/error and expiration, Copy/Download and secret/object URL cleanup. Exercise 1440/1024/375/320, both themes, long names, doubled text, keyboard focus geometry, hit targets and overflow. Run Go API/UI tests, syntax and diff checks. Provide local synthetic preview/screenshots for independent design evaluation before final commit/PR. No merge/deployment authority.

## Final implementation and verification

- Merged freshly fetched `origin/main` at `4660f1363d05c3e77d7f1b7747365084e7839009` after CI tooling landed, preserving the client work in progress without rebasing.
- Replaced separate row Routing/Configuration/menu entries with one Details editor. Creation focuses and reveals the expanded configuration handoff; ordinary Details opens on Device/Routing. A separate adjacent delivery hint explains unsaved-name restrictions and clears on revert/confirmed save.
- Changed-field PATCH writes are sequenced across dialog replacements and recheck session before dispatch. After its own accepted save, the active editor reconciles all confirmed fields together; queue regressions ensure a later unrelated save cannot reintroduce an old name or route. Newer drafts are not overwritten by another dialog's completion.
- Empty-pool presentation handles both `servers_total == 0` and `servers_alive == 0`, including unavailable Clash statistics. Missing targets are explicitly blocked after a successful catalog response; loading/unavailable catalog metadata remains uncertain.
- Updated maintained UI invariants, allocated API lifetime wording, Client screen documentation and browser-suite scope. No backend or shared shell/auth changes.
- Integrated browser runner: all five suites and four runner self-checks passed. Client coverage includes 29 lifetime cases, 25 Details cases, eight viewport/theme matrices plus creation reveal. Final focused Details, layout and embedded UI checks passed after the bounded visual refinements.
- `CGO_ENABLED=0 go test ./internal/api ./ui` passed; final `go test ./ui`, JavaScript syntax and diff checks passed. Local browser environment: Node 24.5.0, pinned Playwright 1.59.1/Chromium. Dependencies remain development-only.
- Layout captures use 1440/1024/375/320 in light/dark, long names, all section bottoms, actual keyboard focus/hit-area geometry and doubled computed text at 1440/320. Native browser zoom, screen-reader output, QR camera scan, actual VPN connectivity, backend cookie delivery and deployed runtime behavior are outside this synthetic verification.
- Loopback preview: `node ui/tests/client-preview.fixture.cjs`. Optional captures: set `CLIENT_SCREENSHOT_DIR` for `client-layout.cjs`. Fixture/helper files are excluded from CI suite discovery. No live credentials or real client profiles are used; no staging/deployment calls were performed.
- Independent design evaluation passed on the second attempt after the two bounded creation/delivery refinements. Fresh 320/375 light/dark captures verified creation loading/error/retry/ready, ordinary-entry focus, name draft/revert/confirmed rename and server filename refresh. The separate final exact-commit review and CI remain required; visual acceptance grants no merge authority.

## Integration closure

This implementation was merged and the designated primary checkout synchronized before
final #249 integrated verification. This record was moved to `tasks/done` during that
verification; original implementation evidence and its limitations above are retained.
