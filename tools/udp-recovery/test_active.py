"""Offline active history, attribution and single socket-owner regressions."""
import threading
import time
import unittest

from active import ActiveSeries, classify_recovery, negative_replies, replay_history, validate_history


def record(send, *, state="response", received=None, sent=True, eof=False, stage="receive"):
    return {"phase_at_dispatch":"recovery", "probe":{"send_monotonic":send,
        "end_monotonic":send+0.1,"state":state,"datagram_sent":sent,
        "response_monotonic":send+0.1 if received is None else received,
        "datagram_sent_monotonic":send+0.001,"error_stage":stage,"error_type":"OSError"},
        "control_before":{"state":"eof" if eof else "open-no-buffer"},
        "control_after":{"state":"eof" if eof else "open-no-buffer"}}


class Clock:
    def __init__(self):self.now=100.0
    def __call__(self):return self.now
    def sleep(self,seconds):self.now+=seconds


class Observer:
    def __init__(self,clock):self.clock=clock;self.calls=[]
    def probe(self,label,timeout):
        start=self.clock();self.calls.append((label,timeout,threading.get_ident()))
        if hasattr(self.clock,"now"):self.clock.now+=0.1
        return {"label":label,"send_monotonic":start,"end_monotonic":self.clock(),
                "state":"response","datagram_sent":True,"response_monotonic":self.clock(),"datagram_sent_monotonic":start}
    def summary(self):return {"calls":len(self.calls)}


class ActiveTests(unittest.TestCase):
    def test_prime_and_tail_gaps_are_included(self):
        self.assertFalse(validate_history([record(6)],0,7)["bounded_attempt_history"])
        self.assertFalse(validate_history([record(1)],0,7)["bounded_attempt_history"])
        self.assertTrue(validate_history([record(1),record(4)],0,5)["bounded_attempt_history"])
    def test_attempts_do_not_claim_successful_sends(self):
        result=validate_history([record(1,sent=False,eof=True,state="error")],0,2)
        self.assertTrue(result["bounded_attempt_history"])
        self.assertFalse(result["bounded_local_send_history"])
        self.assertTrue(result["control_eof_seen"])
    def test_successful_prefix_then_eof_is_distinct_from_active_suffix(self):
        result=validate_history([record(1),record(4,eof=True),record(7,state="timeout")],0,8)
        self.assertFalse(result["bounded_local_send_history"])
        self.assertTrue(result["bounded_prefix_until_observed_loss"])
        self.assertEqual(result["first_observed_loss"]["kind"],"control_eof")
    def test_malformed_observation_without_eof_is_not_a_liveness_loss(self):
        result=validate_history([record(1,state="error")],0,2)
        self.assertFalse(result["bounded_local_send_history"])
        self.assertFalse(result["bounded_prefix_until_observed_loss"])
        self.assertEqual(result["unattributed_observation_errors"],1)
    def test_decode_error_remains_unknown_even_with_control_eof(self):
        result=validate_history([record(1),record(4,state="error",eof=True,stage="decode")],0,5)
        self.assertEqual(result["unattributed_observation_errors"],1)
        self.assertFalse(result["bounded_prefix_until_observed_loss"])
    def test_control_inspection_error_does_not_claim_open_prefix(self):
        item=record(1);item["control_before"]={"state":"error"}
        result=validate_history([item],0,2)
        self.assertFalse(result["bounded_local_send_history"])
        self.assertEqual(result["unattributed_observation_errors"],1)
    def test_unordered_and_unbounded_history_are_refused(self):
        for records,end in [([],1),([record(4),record(3)],5),([record(1)],101)]:
            with self.assertRaises(ValueError):validate_history(records,0,end)
    def test_pre_restore_request_reply_cannot_prove_fresh_recovery(self):
        result=classify_recovery([record(4,received=6),record(7,state="timeout",eof=True)],5,10)
        self.assertEqual(result["matching_post_restore_responses"],0)
        self.assertEqual(result["classification"],"control_closed")
    def test_response_outside_series_end_does_not_count(self):
        result=classify_recovery([record(7,received=12)],5,10)
        self.assertEqual(result["matching_post_restore_responses"],0)
    def test_negative_guard_joins_prior_and_late_ids_to_original_send(self):
        first=record(4,state="late_response");first["probe"]["transaction_id"]="first"
        old=record(1);old["probe"]["transaction_id"]="old"
        series={"records":[old,first],"ledger":{"received":[
            {"transaction_id":"old","received_monotonic":5,"kind":"prior"},
            {"transaction_id":"first","received_monotonic":8,"kind":"prior"},
            {"transaction_id":"first","received_monotonic":12,"kind":"current"}]}}
        out=negative_replies(series,3,10)
        self.assertEqual(len(out),1)
        self.assertEqual(out[0]["original_request"]["transaction_id"],"first")
    def test_negative_guard_preserves_last_unappended_transaction_after_inspection_loss(self):
        item=record(4)["probe"];item["transaction_id"]="last"
        series={"records":[],"available_transactions":[item],"available_received":[
            {"transaction_id":"last","received_monotonic":5,"kind":"current"}]}
        self.assertEqual(len(negative_replies(series,3,8)),1)
    def test_exact_post_restore_response_has_actual_send_drift(self):
        result=classify_recovery([record(5.4)],5,10)
        self.assertEqual(result["classification"],"matching_post_restore_response")
        self.assertAlmostEqual(result["first_send_drift_seconds"],0.4)
    def test_replay_matches_every_original_offset_and_phase_tail(self):
        clock=Clock();observer=Observer(clock)
        original={"prime_end":0,"end_time":8,"records":[record(0.2),record(3.2),record(6.2)]}
        result=replay_history(observer,lambda _:{"state":"open-no-buffer"},original,100,
                              clock=clock,sleep=clock.sleep)
        self.assertEqual(len(observer.calls),3)
        self.assertEqual([round(r["probe"]["send_monotonic"]-100,1) for r in result["records"]],[0.2,3.2,6.2])
        self.assertEqual(clock.now,108)
        self.assertTrue(result["matched_timing"])
        self.assertTrue(all(timeout==1.5 for _,timeout,_ in observer.calls))
    def test_late_replay_is_explicitly_not_matched(self):
        clock=Clock();observer=Observer(clock)
        original={"prime_end":0,"end_time":3,"records":[record(0)]}
        result=replay_history(observer,lambda _:{"state":"open-no-buffer"},original,99,
                              clock=clock,sleep=clock.sleep)
        self.assertFalse(result["matched_timing"])
    def test_immediate_marker_keeps_one_socket_owner(self):
        observer=Observer(time.monotonic);seen=threading.Event()
        original_probe=observer.probe
        def call(label,timeout):
            result=original_probe(label,timeout)
            if len(observer.calls)>=2:seen.set()
            return result
        observer.probe=call
        series=ActiveSeries(observer,lambda _:{"state":"open-no-buffer"},time.monotonic())
        series.start();series.mark("recovery",immediate=True)
        deadline=time.monotonic()+2
        while len(observer.calls)<1 and time.monotonic()<deadline:time.sleep(.001)
        series.mark("recovery",immediate=True)
        self.assertTrue(seen.wait(2))
        result=series.stop()
        self.assertEqual(len({thread for _,_,thread in observer.calls}),1)
        self.assertNotEqual(observer.calls[0][2],threading.get_ident())
        self.assertTrue(result["history"]["bounded_attempt_history"])
        with self.assertRaises(RuntimeError):series.start()


if __name__=="__main__":unittest.main()
