# Retained SOCKS UDP recovery probe

This inert Python module observes one borrowed, already-open SOCKS UDP association.
It never opens/replaces a socket, launches a process, changes routing or installs a
fault. It is separate from the daemon and has no runtime integration.

`RetainedSTUN` pins the exact UDP/control objects, file descriptors, local/control
peer endpoints and relay. Each bounded numeric STUN request has a unique 96-bit
transaction ID. The ledger retains current, prior and unknown valid response IDs
across timeouts. A late response never satisfies a newer request. Malformed or
foreign packets are observation errors, not successful traffic or clean timeouts.
Response timestamps, actual elapsed time and deadline membership are recorded;
scheduling delays must not be reported as a response within the nominal window.

The intended series starts requests at offsets 0, 7 and 27 seconds with up to three
seconds per request. It samples windows ending near 3, 10 and 30 seconds rather
than continuously observing every instant. Private orchestration must record its
actual clock origin and drift, concurrent fresh controls and comparable no-fault
idle duration. The control-first gate uses separately primed five- and twelve-second
idle cases before considering any new outage. Passive control-socket EOF is
recorded separately from a UDP transaction timeout. See [task #350](../../tasks/350-retained-udp-recovery.md) for scope,
cleanup, fault controls and pending live acceptance. A single STUN transaction
series does not prove application/media survival or a general recovery bound.

Run the offline fake-socket checks:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tools/udp-recovery -p 'test_*.py' -v
```
