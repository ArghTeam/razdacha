"""Single-owner active retained-UDP history and matched replay; inert on import."""
from __future__ import annotations

import copy
import threading
import time

INTERVAL_SECONDS = 3.0
PROBE_SECONDS = 1.5
MAX_GAP_SECONDS = 5.0
MAX_HISTORY_SECONDS = 100.0
MATCH_TOLERANCE_SECONDS = 0.5
MAX_RECORDS = 80


def validate_history(records, prime_end, end_time=None):
    if not records or len(records) > MAX_RECORDS:
        raise ValueError("active history count is empty or outside the bound")
    attempts = [r["probe"]["send_monotonic"] for r in records]
    end_time = records[-1]["probe"]["end_monotonic"] if end_time is None else end_time
    times = [prime_end]+attempts+[end_time]
    gaps = [b-a for a,b in zip(times,times[1:])]
    if any(gap < 0 for gap in gaps) or end_time-prime_end > MAX_HISTORY_SECONDS:
        raise ValueError("active history is unordered or exceeds its lifetime bound")
    sent_times = []
    losses = []
    unknown_errors = 0
    for r in records:
        item = r["probe"]
        if item.get("datagram_sent") is True:
            sent = item["datagram_sent_monotonic"]
            if not item["send_monotonic"] <= sent <= item["end_monotonic"]:
                raise ValueError("successful local-send timestamp is outside its request bounds")
            sent_times.append(sent)
        else:
            losses.append({"kind":"local_datagram_send_failed","observed_monotonic":item["end_monotonic"]})
        eof = False
        if any(r["control_"+side].get("state") not in {"open-no-buffer","eof"} for side in ("before","after")):
            unknown_errors += 1
        for side in ("before","after"):
            if r["control_"+side].get("state") == "eof":
                eof = True
                losses.append({"kind":"control_eof", "observed_monotonic":r.get("control_"+side+"_monotonic",item["send_monotonic"] if side=="before" else item["end_monotonic"])})
        if item["state"] == "error":
            local_receive_loss = item.get("error_stage") == "receive" and item.get("error_type") in {
                "OSError","ConnectionResetError","ConnectionRefusedError","BrokenPipeError","ConnectionAbortedError"}
            known_send_loss = item.get("error_stage") == "send" and item.get("datagram_sent") is False
            if not known_send_loss and not (eof and local_receive_loss):
                unknown_errors += 1
    loss = min(losses,key=lambda x:x["observed_monotonic"]) if losses else None
    prefix_end = loss["observed_monotonic"] if loss else end_time
    prefix_sends = [t for t in sent_times if t <= prefix_end]
    prefix_times = [prime_end]+prefix_sends+[prefix_end]
    prefix_gaps = [b-a for a,b in zip(prefix_times,prefix_times[1:])]
    send_times = [prime_end]+sent_times+[end_time]
    send_gaps = [b-a for a,b in zip(send_times,send_times[1:])]
    all_sent = len(sent_times)==len(records)
    full = all_sent and max(send_gaps)<=MAX_GAP_SECONDS and loss is None and not unknown_errors
    prefix = bool(loss and prefix_sends and max(prefix_gaps)<=MAX_GAP_SECONDS and not unknown_errors)
    return {"request_count":len(records), "maximum_attempt_gap_seconds":max(gaps),
            "bounded_attempt_history":max(gaps)<=MAX_GAP_SECONDS,
            "all_datagrams_sent":all_sent,"maximum_successful_send_gap_seconds":max(send_gaps),
            "maximum_prefix_send_gap_seconds":max(prefix_gaps),
            "bounded_local_send_history":full,"bounded_prefix_until_observed_loss":prefix,
            "first_observed_loss":loss,"send_offsets":[t-prime_end for t in attempts],
            "control_eof_seen":any(r[side].get("state")=="eof" for r in records
                                   for side in ("control_before","control_after")),
            "unattributed_observation_errors":unknown_errors,
            "observation_errors":sum(r["probe"]["state"]=="error" for r in records)}


def classify_recovery(records, origin, end_time):
    after = [r for r in records if origin <= r["probe"]["send_monotonic"] <= end_time]
    replied = [r for r in after if r["probe"]["state"] == "response"
               and r["probe"].get("response_monotonic", float("inf")) <= end_time]
    eof = any(r[side].get("state") == "eof" for r in after
              for side in ("control_before", "control_after"))
    verdict = "matching_post_restore_response" if replied else "control_closed" if eof else "no_matching_post_restore_response"
    return {"classification": verdict, "post_restore_attempts": len(after),
            "matching_post_restore_responses": len(replied), "control_eof_seen": eof,
            "first_send_drift_seconds": after[0]["probe"]["send_monotonic"]-origin if after else None,
            "first_matching_send_after_origin_seconds": replied[0]["probe"]["send_monotonic"]-origin if replied else None}


def negative_replies(series, begin, end):
    """Join every received ID to its original request, including late/prior replies."""
    transactions = {t["transaction_id"]:t for t in series.get("ledger",{}).get("transactions",series.get("available_transactions",[]))}
    transactions.update({r["probe"]["transaction_id"]:r["probe"] for r in series["records"]})
    output = []
    for reply in series.get("ledger",{}).get("received",series.get("available_received",[])):
        original = transactions.get(reply["transaction_id"])
        if original is None or original.get("datagram_sent") is not True:
            continue
        if (begin <= original["send_monotonic"] <= original["datagram_sent_monotonic"]
                <= reply["received_monotonic"] <= end):
            output.append({"original_request":original,"received_reply":reply})
    return output


class ActiveSeries:
    """Only this worker calls probe after priming; phase markers never read sockets."""

    def __init__(self, observer, control_state, prime_end, *, clock=time.monotonic):
        self.observer = observer
        self.control_state = control_state
        self.prime_end = prime_end
        self.clock = clock
        self.records = []
        self.markers = []
        self.error = None
        self.error_monotonic = None
        self._condition = threading.Condition()
        self._stop = False
        self._immediate = False
        self._phase = "setup"
        self._thread = None

    def start(self):
        if self._thread is not None:
            raise RuntimeError("active series cannot restart")
        self._thread = threading.Thread(target=self._run, name="retained-udp-352", daemon=True)
        self._thread.start()

    def mark(self, phase, *, immediate=False):
        with self._condition:
            marker = {"phase": phase, "local_monotonic": self.clock(), "immediate": immediate}
            self.markers.append(marker)
            self._phase = phase
            self._immediate = self._immediate or immediate
            self._condition.notify_all()
        return marker

    def _run(self):
        due = self.clock()
        try:
            while True:
                with self._condition:
                    while not self._stop and not self._immediate and self.clock() < due:
                        self._condition.wait(min(due-self.clock(), PROBE_SECONDS))
                    if self._stop:
                        return
                    now = self.clock()
                    if now-self.prime_end > MAX_HISTORY_SECONDS or len(self.records) >= MAX_RECORDS:
                        raise RuntimeError("active worker lifetime/count bound reached")
                    phase = self._phase
                    immediate = self._immediate
                    self._immediate = False
                before = self.control_state(self.observer)
                before_at = self.clock()
                result = self.observer.probe("active-"+str(len(self.records))+"-"+phase,
                                             timeout=PROBE_SECONDS)
                after = self.control_state(self.observer)
                after_at = self.clock()
                item = {"phase_at_dispatch": phase, "immediate_dispatch": immediate,
                        "scheduled_monotonic": due, "probe": result,
                        "control_before": before, "control_after": after,
                        "control_before_monotonic":before_at,"control_after_monotonic":after_at}
                with self._condition:
                    self.records.append(item)
                    self._condition.notify_all()
                due = result["send_monotonic"]+INTERVAL_SECONDS
        except BaseException as exc:
            self.error = type(exc).__name__+": "+str(exc)
            self.error_monotonic = self.clock()

    def stop(self):
        with self._condition:
            self._stop = True
            stop_requested = self.clock()
            self._condition.notify_all()
        if self._thread is not None:
            self._thread.join(PROBE_SECONDS+2)
            if self._thread.is_alive():
                raise RuntimeError("retained worker did not stop within its bounded receive")
        end_time = max(stop_requested, self.records[-1]["probe"]["end_monotonic"]) if self.records else stop_requested
        history = validate_history(self.records, self.prime_end, end_time) if self.records else {
            "request_count":0,"bounded_attempt_history":False,"bounded_local_send_history":False,
            "maximum_attempt_gap_seconds":end_time-self.prime_end,"all_datagrams_sent":False,
            "send_offsets":[],"control_eof_seen":False,"observation_errors":0}
        result = {"records":copy.deepcopy(self.records),"markers":copy.deepcopy(self.markers),
                  "prime_end":self.prime_end,"end_time":end_time,"history":history,
                  "worker_error":self.error,"worker_error_monotonic":self.error_monotonic}
        try:result["ledger"] = self.observer.summary()
        except Exception as exc:
            result["ledger_error"] = type(exc).__name__+": "+str(exc)
            result["available_transactions"] = copy.deepcopy(list(getattr(self.observer,"transactions",{}).values()))
            result["available_received"] = copy.deepcopy(getattr(self.observer,"received",[]))
        ledger_transactions = result.get("ledger",{}).get("transactions",result.get("available_transactions",[]))
        recorded = {r["probe"].get("transaction_id") for r in self.records}
        extra_unknown = sum(t.get("state") == "error" and t.get("transaction_id") not in recorded
                            for t in ledger_transactions)
        result["history"]["unattributed_worker_error"] = self.error is not None
        if self.error:
            result["history"]["bounded_local_send_history"] = False
            result["history"]["bounded_prefix_until_observed_loss"] = False
        if extra_unknown:
            result["history"]["unattributed_observation_errors"] += extra_unknown
            result["history"]["bounded_local_send_history"] = False
            result["history"]["bounded_prefix_until_observed_loss"] = False
        return result

    def wait_first_response(self, timeout=3.0):
        """Require a positive worker transaction before fault preparation."""
        deadline = self.clock()+timeout
        with self._condition:
            while not self.records and self.error is None and self.clock()<deadline:
                self._condition.wait(max(0.001,deadline-self.clock()))
            if not self.records or self.records[0]["probe"]["state"] != "response":
                raise RuntimeError("active worker did not establish a healthy first transaction")
            return copy.deepcopy(self.records[0])



def replay_history(observer, control_state, original, prime_end, *, clock=time.monotonic,
                   sleep=time.sleep, progress=None):
    """Replay every measured send offset with a new primed association and IDs."""
    validation = validate_history(original["records"], original["prime_end"], original["end_time"])
    records = []
    for index,(record,offset) in enumerate(zip(original["records"], validation["send_offsets"])):
        scheduled = prime_end+offset
        remaining = scheduled-clock()
        if remaining > 0:
            sleep(remaining)
        before = control_state(observer)
        before_at = clock()
        result = observer.probe("replay-"+str(index)+"-"+record["phase_at_dispatch"],
                                timeout=PROBE_SECONDS)
        after = control_state(observer)
        after_at = clock()
        item = {"phase_at_dispatch": record["phase_at_dispatch"], "original_offset_seconds": offset,
                "scheduled_monotonic": scheduled,
                "send_drift_seconds": result["send_monotonic"]-scheduled,
                "probe": result, "control_before": before, "control_after": after,
                "control_before_monotonic":before_at,"control_after_monotonic":after_at}
        if record["probe"].get("datagram_sent") is True and result.get("datagram_sent") is True:
            expected_completion = prime_end + record["probe"]["datagram_sent_monotonic"]-original["prime_end"]
            item["local_send_completion_drift_seconds"] = result["datagram_sent_monotonic"]-expected_completion
        records.append(item)
        if progress:
            progress(records)
    target_end = prime_end + original["end_time"]-original["prime_end"]
    remaining = target_end-clock()
    if remaining > 0:
        sleep(remaining)
    maximum_drift = max(abs(r["send_drift_seconds"]) for r in records)
    completion_drifts = [abs(r["local_send_completion_drift_seconds"]) for r in records if "local_send_completion_drift_seconds" in r]
    maximum_completion_drift = max(completion_drifts) if completion_drifts else None
    return {"records": records, "prime_end": prime_end,
            "end_time": clock(), "history": validate_history(records, prime_end, clock()),
            "maximum_abs_send_drift_seconds": maximum_drift,
            "maximum_abs_local_send_completion_drift_seconds":maximum_completion_drift,
            "matched_timing": maximum_drift <= MATCH_TOLERANCE_SECONDS and maximum_completion_drift is not None and maximum_completion_drift <= MATCH_TOLERANCE_SECONDS,
            "all_matching_responses": all(r["probe"]["state"] == "response" for r in records),
            "ledger": observer.summary()}
