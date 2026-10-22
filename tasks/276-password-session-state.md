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
