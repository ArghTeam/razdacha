# 0042. Explain committed client web policy without probes

Date: 2026-11-07 · Status: accepted · Tracker: #339

## Decision

Extend ADRs 0023–0026, 0028, 0032, 0036–0037 and 0040–0041; supersede none. Provide an authenticated, no-store, read-only domain explanation for one existing client and WG or REALITY ingress, explicitly scoped to web TCP/443. This is conditional applied policy, never an observed connection or a quality test.

The publication gate supplies a coherent bounded copy of committed policy and frozen list contents. Compilation happens outside the gate, using evidence collected by the existing single generator pass. Recheck publication markers and current eligibility before returning. Uninitialized, uncertain, guarded or changed authority yields unknown. Live revocations and committed/live REALITY admission remain authoritative; pending edits are reported separately.

Manual/plain domains use exact suffix boundaries and destination OR semantics. Earlier unresolved address/native predicates remain possible candidates before a later known result. Whole-client exits retain precedence and unavailable exits reject. Native originals are never flattened into guessed matches. Explain generated conditional gateway A-query actions separately, retaining the captured-DNS C1 priority boundary: an earlier direct route does not suppress a later DNS reject/FakeIP action.

One active bounded request prevents cancellation from multiplying compiler work. No DNS, HTTP or controller probes, policy/auth writes, listener context, live REALITY UUID/server-private-key reads or client profile/config export are permitted. Private committed snapshots can contain WG/manual-tunnel keys and remain internal. Return only explicit allowlisted fields; never include source URLs, raw configuration or endpoint credentials.

The Clients dialog owns its session, modal and request lifetime. Input changes, close, replacement, navigation and session transitions invalidate old responses. All displayed values are escaped.

## Limits

No DNS resolution, private-address outcome, dynamic name recovery, client traffic, runtime availability, end-to-end routing, DPI resistance, UDP/media quality or actual query is measured. The domain policy assumes an eligible domain-bearing request and public resolved destination; private/gateway resolution guards may reject first. Numeric/FakeIP literal and arbitrary port inputs are outside version 1. Subsequent observation work remains milestone 1b of roadmap #338.
