# Observation integrity and plain-list retention

Tracker: #328; audit findings F09, F10, F13 under #324.

## Intended behavior

- Classify every controller row before filtering REALITY observations. Malformed or
  unclassifiable rows invalidate the snapshot; valid other inbounds need no REALITY
  authentication. Empty snapshots observe zero; absent byte counters remain unknown.
- WG row bytes use nonnegative safe integers. Unknown stays Unknown; zero stays 0 B.
- Retire obsolete plain-list memory/status after pending and committed consumers
  release it. Failed Apply retains committed sources. Removed in-flight source
  incarnations cannot repopulate memory. Native immutable authority is untouched.

## Verification

Synthetic local HTTP fixtures, concurrent manager regressions, actual embedded UI,
Go/race checks and Linux static checks. No provider or deployment mutation.

## Checkout

Primary: /Users/romanzhigalkin/Web/razdacha; remote: origin; target: main.
Fresh fetched base: 8dc7cdbe50d6b6639d06cabfc7890fa75d7172cf.
Worktree: .codex/worktrees/fix-328-observation-list-retention.
Branch: fix/328-observation-list-retention.
