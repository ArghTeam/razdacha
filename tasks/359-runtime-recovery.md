# Independent runtime recovery

Tracker: #359

Primary checkout `/Users/romanzhigalkin/Web/razdacha`, remote/base `origin/main`,
freshly fetched SHA `48e41f2c6adac41ed13482a4daf5166c646c9fc7`.

A managed independent runtime restart recovers committed routing through serialized
checked publication. Only a successful managed startup guard requests repair.
Intentional guards and uncertain publications retain administrator recovery.
Pending database policy never becomes automatic authority. Live authentication,
WG admission and native artifact validation remain mandatory.

Acceptance covers marked startup repair, intentional guard refusal, pending policy,
corrupt authority and failed repair without repeated restarts. Host acceptance is
recorded independently from deterministic checks.

## Verification

| Acceptance | Result |
| --- | --- |
| Managed startup requests checked committed recovery | Passed deterministic regression |
| Intentional, uncertain and uninitialized state stays closed | Passed deterministic regression |
| Pending policy stays pending and live revocation remains authoritative | Passed deterministic regression |
| Corrupt/stale authority, failed guard, failed reload/readiness remain closed | Passed deterministic regression |
| Failed repair cannot repeatedly restart the runtime | Passed deterministic regression |
| Affected daemon, ingress guard and packaging packages | Passed `go test` |
| Recovery regressions with race detector | Passed `go test -race` |
| Candidate deployment and independent runtime crash on staging | Pending separate operator acceptance |

Production host operations are outside this implementation. No host changes were
performed by the implementation worker.
