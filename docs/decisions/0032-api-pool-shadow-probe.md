# 0032. API pool admission and a shadow-only operator probe

Date: 2026-10-27. Status: experimental; clarifies 0015 and 0020.

## Context

The provider has published a JSON API at `https://vpnkeys.me/api/v1/keys`
and `https://freevpnkeys.com/api/v1/keys`. This new contract is distinct from
the retired HTML catalogs recorded in ADR 0015. Provider observations are not
measurements from this gateway and cannot establish client or media usability.

## Decision

Admit only the official HTTPS hosts at the exact API path, as one bounded JSON
batch (100 by default, maximum 240). Mirror retries preserve every query filter;
an empty valid response never authorizes weaker filters. Legacy HTML paths remain
retired. This clarifies the host retirement in ADR 0015 without changing its
historical evidence or other source restrictions.

API keys retain credentials, transport and query parameters in identity; only
the fragment is removed and preserved separately as the source title. Source
country is normalized and checked alongside title country: either can exclude
a member. This extends ADR 0020 conservatively, without GeoIP or trusting a
source security claim over URI encryption and parser checks. Explicit blocked
observations override positive provider verification flags. Provider latency is
an observation from the provider, never a gateway health result.

Expose this increment through an explicitly invoked operator probe. Default
mode fetches, parses and filters only. Optional checking uses a pinned, explicit
sing-box binary and `Applier.Prepare` over a synthetic snapshot in private owned
temporary storage. It never commits, reloads, starts a runtime, reads installed
state, allocates listeners or changes networking. Reports contain counts and
stage outcomes, never credentials, URI remarks or raw checker diagnostics.

## Consequences

ADR 0018 remains in force: one built-in igareck pool, initially disabled. No
installer prompt, automatic activation, personal fallback or new installed pool
profiles are introduced. Catalog/config acceptance is not connectivity, UDP,
throughput, media or service proof. Staging validation remains required before
installer work. A live sidecar requires a separate lifecycle/security review.

Source contract: [provider API](https://github.com/TelegaVPN/free-vpn-keys/blob/main/API.md).
