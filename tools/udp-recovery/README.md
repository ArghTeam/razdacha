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

Legacy sample-window constants start requests at offsets 0, 7 and 27 seconds.
Those idle-separated windows were not used in the accepted active-history run;
`active.py` instead retains a continuous nominal three-second cadence. Private orchestration must record its
actual clock origin and drift, concurrent fresh controls and comparable no-fault
idle duration. The control-first gate uses separately primed five- and twelve-second
idle cases, with one post-idle transaction each and two independent pairs, before
considering any new outage. Passive control-socket EOF is
recorded separately from a UDP transaction timeout. See [task #350](../../tasks/done/350-retained-udp-recovery.md) for scope,
cleanup, conditional fault controls and completed bounded idle acceptance. A single STUN transaction
series does not prove application/media survival or a general recovery bound.

Run the offline fake-socket checks:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tools/udp-recovery -p 'test_*.py' -v
```


The completed no-fault characterization used single post-idle probes after
5/12/5/12 and 30/45 seconds. The short cases responded; both long cases already
had SOCKS control EOF and then timed out while fresh associations worked. This
establishes an idle confound, not the closing hop, a precise timeout or active-flow
outage recovery. The conditional 3/10/30-second outage series was not executed.


## Active history (#352)

`active.py` adds a single owner for retained socket reads/writes, a nominal
three-second request cadence with at most 1.5 seconds per transaction, and an
immediate marker for first verified fault absence. Actual dispatch drift and
attempt/local-send gaps are recorded; local send completion is not remote-carrier
liveness. A timestamped successful-send prefix ending in observed EOF or send
failure is distinct from continued activity. Decode/foreign-frame errors remain
unknown even if EOF is also observed.

Matched replay uses every measured retained request offset and timeout with new
transaction IDs, including setup, fault and recovery traffic history. Full received
IDs are joined to their original request times for negative and recovery checks;
A late/prior receipt proves only its original transaction; that original send
must be post-origin to support post-restoration recovery evidence. See
[task #352](../../tasks/done/352-active-udp-recovery.md) for the bounded controller
contract and bounded RAC acceptance. This remains test code, not automatic
failover, client reconnection or a daemon feature.


The completed RAC case kept 22 full local sends at no more than 3.010489-second
send gaps, retained open controls and received all 10 post-origin transaction
replies within its fixed 30-second window. The matched no-fault replay answered
all 22 requests. This is one finite outer-leg failure/recovery case, not a product
fix or general recovery guarantee; artifact provenance and timing uncertainty are
in the completed task record.
