# 0036. Deliver saved REALITY profiles before activation

Date: 2026-11-05 · Status: **accepted** · Tracker: [#308](https://github.com/ArghTeam/razdacha/issues/308)

## Context

Requiring Apply before obtaining a profile prevents administrators from preparing a
client before activation. REALITY itself does not require this ordering. Profile
possession and runtime admission are different capabilities. The previous delivery
contract in ADR 0032 joined them unnecessarily.

## Decision

Supersede only ADR 0032's applied-only profile delivery requirement. Its credential,
revocation, guard, policy publication and runtime admission contracts remain in force.
Numbers 0033–0035 are reserved by the RAC staging lineage; this decision uses 0036.

Dedicated authenticated, no-store profile endpoints may export current saved client
credentials before Apply. A separate private, read-only transaction reads current
client flags/revision/UUID, settings and public server identity under the publication
gate. It creates no keys, persists no admission and invokes no runtime operation.
`RealityRuntime` remains the committed-admission/live-credential intersection.

Active clients retain committed connection settings even when settings have saved
edits. Pending clients use validated saved connection settings with the current live
UUID and public identity. Global/client disable, deleted or missing credentials,
missing identity, invalid configuration, uncertain publication and guarded ingress
refuse delivery. Successful key replacement can export its new pending credential;
failed reconciliation remains guarded and cannot export either credential.

Keep `reality_profile_status` as activation status. Add the non-secret boolean
`reality_profile_downloadable`. Build URI, complete sing-box JSON and response
metadata from the same material resolver. Headers `X-Razdacha-Reality-State`
(`pending` or `available`) and `X-Razdacha-Reality-Revision` (positive integer)
accompany raw importable bodies. Ordinary inventory never contains a bearer or
server private key. Active availability is not a measured client handshake.

The panel requires downloadable inventory and matching response revision, preserving
session, modal, draft and mutation epoch checks. A persistent **Works after Apply**
notice accompanies pending QR, copy and both downloads, including a server pending
response received against older available inventory. **Close and go to Apply**
only closes/clears the modal and focuses the existing global action, with unsaved
edits blocking navigation. Apply still publishes all saved changes explicitly.

Connection methods are independent profiles sharing client routing rules. Selecting
both does not implement automatic fallback.

## Consequences and verification

Administrators can import a real profile immediately, then explicitly activate it.
Profiles may contain future saved endpoints; changing those settings before Apply
can require a fresh export. Already active clients continue receiving committed
endpoints. An exported bearer remains sensitive even while not admitted.

Tests prove real pending UUID delivery without authority/config/runtime mutation,
identical connection material after unchanged Apply, current revision handling,
rotation/revocation guards and coherent HTTP metadata. Synthetic browser tests cover
pending creation/delivery notices and existing lifetime controls; these do not prove
operating-system TUN startup or end-to-end transport connectivity.
