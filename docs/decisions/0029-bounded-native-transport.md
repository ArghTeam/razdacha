# 0029. Bounded native transport address rules

Date: 2026-10-20 · Status: accepted; extends ADR 0025

Native projection v2 admits positive TCP/UDP network and destination port/range
conditions in address-only branches. Positive AND intersects address envelopes;
OR unions them. AND retains the intersection of unconditional address branches
separately, including nested address/transport OR expressions; it never narrows
existing all-protocol interception to TCP/UDP. Transport-only intermediate nodes may
cover IPv4, but every final interception envelope must be bounded. Contextual inversion, domain/context mixing,
source/process/unknown fields and unbounded final envelopes remain unsupported.

Original immutable routing bytes retain exact network and destination-port semantics.
DNS contains only the independently separable domain expression. Transport address
envelopes use a separate nft set marked only for TCP/UDP, after the existing DNS and
local-address exemptions. Ordinary address branches retain their existing behavior.
Port/protocol mismatches may enter sing-box and follow normal ordered routing; they
never gain the selected native action merely from the interception envelope.

Projection v1 authority is verified with its original admission contract. New accepted
manifests use v2 and committed generation envelopes use v4, preventing older daemons
from silently ignoring transport interception fields. Recovery rederives both address
projections and DNS from pinned bytes. Existing budgets, guarded publication, immutable
artifacts and last-good refresh behavior remain unchanged.


## Validation boundary

Source `5e4995a` passed the pinned 1.12.25 JSON/SRS isolated Linux matrix, including
positive synthetic-exit UDP delivery, protocol/port/address controls, rule ordering,
peer scope, raw protocol isolation, DNS, unavailable-target and client-wide exit
precedence, and committed recovery. Twenty-four receiver barriers reported zero
drops. Host application state remained unchanged and owned fixture artifacts were
removed. The [implementation record](../../tasks/done/235-native-transport.md)
pins the source, test ELF and private evidence hashes.

These are finite veth/synthetic-exit observations, not encrypted WireGuard or
provider-tunnel validation, a lossless-capture claim, or deployment approval.
