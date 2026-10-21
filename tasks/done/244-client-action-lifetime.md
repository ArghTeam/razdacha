# Client action lifetime

Issue: #244. Parent: #242.

## Required behavior

Creation is single flight per form across keyboard and pointer input. Same-session creation and mutation results remain visible after ordinary close or replacement. Responses from an earlier session cannot mutate inventory, pending state, notifications or confirmation dialogs. Preflight confirmation and feedback also expire when another client action or modal replaces the interaction. Configuration secrets remain modal-owned; selected-rule orphan instructions remain explicit.

## Checkout preflight

Designated primary: `/Users/romanzhigalkin/Web/razdacha`, branch `main`.
Remote: `origin` (`git@github.com:ArghTeam/razdacha.git`), target: `main`.
Freshly fetched base: `f2d7480173ba4b8e9c2545f4fdbc0d23fc63c836`.
Feature checkout: `/Users/romanzhigalkin/Web/razdacha/.codex/worktrees/fix/client-action-lifetime`.
Feature branch: `fix/client-action-lifetime`.

## Acceptance

Chromium regressions cover duplicate creation, ordinary-close creation, configuration replacement cleanup, same-session success/error and delayed rename/toggle/delete/preflight/follow-up callbacks after logout/login or expiration. Only synthetic intercepted API responses and loopback assets are used. Run relevant Go API/UI checks. No staging or deployed host mutations.

## Implemented and verified

- The disabled create button is the synchronous form gate for keyboard and pointer paths; missing/replaced forms are ignored. Server-completed same-session creation still updates inventory and pending state.
- Session guards cover all menu mutation and deletion rule-loading callbacks. A separate action/modal revision suppresses obsolete feedback and confirmations while preserving legitimate same-session state updates. Rename and toggle resolve the current inventory object when completing.
- Configuration fetch callbacks also verify their initiating session; modal cleanup is unchanged.
- `NODE_PATH=<directory containing playwright> node ui/tests/client-actions.cjs`: 30 synthetic Chromium cases passed. The optional script resolves Playwright normally and adds no daemon build dependency.
- `CGO_ENABLED=0 go test ./internal/api ./ui`: passed. JavaScript syntax and `git diff --check`: passed.
- Maintained API invariants describe the creation gate and session/interaction lifetime contract. No backend, live cookie-delivery, deployed networking or staging behavior is inferred from intercepted browser responses.
