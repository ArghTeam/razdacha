# 0035. Authenticated first-login web setup

Date: 2026-10-28. Status: experimental; supersedes the no-primary-wizard
restriction in ADR 0014 and the terminal-first assumption in ADR 0033.

## Context

The user requested actual browser onboarding after initial password login.
Terminal choices and a downloadable profile do not provide that guided path.
The public authenticated bootstrap panel from ADR 0034 remains required.

## Decision

Fresh setup persists a pending marker with the initial client identity, then
defers the default free-tunnel choice to a first-login wizard. Explicit CLI/env
automation remains supported and is recognized without duplicate client/rules.
Absent metadata on legacy installations means completed, not inferred from
client count. Authenticated explicit reopening only changes this marker.

The browser guides choice, truthful review, explicit configuration/apply, existing
client profile delivery and persistent completion. Auto is the default country.
No mount, selection, polling or cancellation publishes policy. Reopening or Skip
never silently disables existing exits or rules. The server owns source URLs and
validates changes to the one built-in pool and owned first-client starter rule.
Existing personal exits remain excluded.

Configuration rejects unrelated pending edits. Transactional policy fingerprints
and the existing daemon publication gate protect configure/retry/apply against
concurrent unrelated policy changes. Fingerprints exclude only mutable pool
membership observations, never manual-tunnel credentials or routing policy. Normal guarded
publication and source authority remain mandatory; missing sources produce a
visible retryable failure, never a bypass. Completion follows accepted Apply.

Authenticated profile delivery reuses the existing client API and QR/download
components, loading secrets only on explicit delivery. Aggregate Clients counters
reuse finite numeric API observations, describe current-client/interface-reset
scope and represent missing data as unknown. They are not lifetime history.

## Consequences

No frontend build dependency, unauthenticated access, additional pool topology or
personal fallback is introduced. The old ordinary preset remains unchanged.
Existing installed clients/rules/profiles survive upgrades; the current staging
installation may explicitly reopen setup without changing routing. Config
acceptance does not prove client, media, throughput or UDP usability.
