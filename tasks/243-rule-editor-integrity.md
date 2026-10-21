# Rule editor integrity

Issue: #243. Intended behavior: preserve unavailable community IDs and read-only API-managed URL sources; never substitute tunnel targets; scope overlap to intersecting shared-rule clients; retain actionable errors; guard asynchronous completions by session and modal lifetime while preserving same-session pending mutations. Selected empty disabled orphan rules and existing chain/DNS behavior remain supported.

Primary checkout: /Users/romanzhigalkin/Web/razdacha. Remote: origin (git@github.com:ArghTeam/razdacha.git). Target: main. Freshly fetched base: f2d7480173ba4b8e9c2545f4fdbc0d23fc63c836.
Feature checkout: /Users/romanzhigalkin/Web/razdacha/.codex/worktrees/fix/rule-editor-integrity; branch: fix/rule-editor-integrity.

Acceptance: actual Chromium with local synthetic API covers missing/partial catalogs, URL-only rules, unavailable targets, scoped overlap, stale save/load/error completion, validation and failures. No host/staging traffic or daemon build dependency on Node.

## Implementation and verification

- Unavailable community rows remain checked with explicit unavailable badges; selected counts include absent IDs without a misleading catalog denominator. Existing URL sources are escaped inert text, omitted from PATCH.
- Target and chain selectors preserve missing references and disabled choices. New rules require a deliberate target. Empty-pool and failed-health hints distinguish configured rejection from an unhealthy route; neither claims direct fallback.
- Overlap uses intersecting enabled shared-rule clients; inventory and draft warnings describe partial scope accurately.
- Save and inventory loads guard session/revision ownership. Same-session successful mutations retain pending state after close/replacement; errors remain local to the originating editor.
- Checks: 14 synthetic Chromium browser cases passed; `CGO_ENABLED=0 go test ./ui` passed; ES-module syntax and `git diff --check` passed. Optional suite command: `NODE_PATH=<directory containing playwright> node ui/tests/rule-integrity.cjs`. `UI_SCREENSHOTS_DIR` optionally captures visible final editors.
- Maintained context: `context/ui/invariants.md`, Rule editor integrity.

## Boundaries

The suite runs committed screen/shell/API modules in a local HTML harness with intercepted synthetic API responses; it does not authenticate against the daemon, test cookie delivery, deploy or exercise networking. Missing IDs and unavailable-chain payload preservation do not imply the backend accepts invalid references. The API remains authoritative. Store validation requires a nonempty tunnel even for disabled rules (`internal/store/model.go`); an empty-target orphan therefore requires an explicit target or action change before Save. No backend contract is changed.
