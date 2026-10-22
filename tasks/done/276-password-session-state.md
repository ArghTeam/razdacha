# Password-reset session issuance barrier (#276)

## Goal
A successful password reset must revoke issued sessions and prevent in-flight verification of an older password state from issuing a valid session afterward, including across independent SQLite connections.

## Design
Keep ADR 0009 authentication and schema unchanged. Store a random password-state generation in private settings, rotated transactionally with the hash and session revocation on every reset (including identical hashes). Read hash and generation together after verification admission. Insert the session with one conditional SQL statement checking the verified hash and generation. A stale verified state returns unauthorized without a cookie; infrastructure failures remain internal errors. Legacy hashes without a generation remain readable until the next reset. Ordinary settings APIs never expose or overwrite authentication keys.

## Acceptance
- Deterministic reset between verification and session insertion fails on the baseline and passes after the change.
- Independent connections reject stale, same-hash-reset and ABA password states; reset after insertion revokes sessions.
- Correct/wrong passwords, logout, expiration and semaphore admission remain covered.
- Focused tests, race tests and static checks pass; no installed network state is touched.

## Checkout
Primary: `/Users/romanzhigalkin/Web/razdacha`; origin: `git@github.com:ArghTeam/razdacha.git`; target: `main`; verified fetched base: `e262cc69d573a8e97a54b47cf5d700716190abf9`.


## Completion

Merged as [PR #278](https://github.com/ArghTeam/razdacha/pull/278), commit
`af68bd8bef4258c4fdd05b7d89113b089bd92fc3`. Independent review accepted the
password-state issuance barrier. Deterministic stale verification, same-hash/ABA,
independent-connection and ordinary authentication cases were verified; hosted tests,
race/static validation and all six hosted CI checks passed. The change preserves the
schema and ADR 0009 contract. Deployment is recorded separately from implementation.


## Delivery

Included in the verified staging v0.11.0-rc.7 release. See the shared
[staging acceptance record](280-dashboard-design-system.md#staging-acceptance) for
exact running identity, preserved-state evidence and browser/operator boundaries.
This delivery record does not claim replay of the security/MTU test matrix on a live host.
