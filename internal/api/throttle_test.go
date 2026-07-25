package api

import (
	"testing"
	"time"
)

func TestThrottleBlocksAfterSeries(t *testing.T) {
	now := time.Now()
	th := newThrottle(func() time.Time { return now })

	var last time.Duration
	for i := 1; i < maxLoginFails; i++ {
		delay := th.fail("203.0.113.7")
		if delay < last {
			t.Errorf("задержка не растёт: попытка %d дала %v после %v", i, delay, last)
		}
		last = delay
		if left := th.retryAfter("203.0.113.7"); left != 0 {
			t.Fatalf("блокировка на %d-й попытке, ожидалась после %d", i, maxLoginFails)
		}
	}

	th.fail("203.0.113.7")
	if left := th.retryAfter("203.0.113.7"); left <= 0 {
		t.Fatalf("после %d неудач блокировки нет", maxLoginFails)
	}
	if left := th.retryAfter("198.51.100.4"); left != 0 {
		t.Error("заблокирован чужой адрес")
	}

	now = now.Add(loginBlock + time.Second)
	if left := th.retryAfter("203.0.113.7"); left != 0 {
		t.Errorf("блокировка не истекла: осталось %v", left)
	}
}

func TestThrottleDelayCapped(t *testing.T) {
	now := time.Now()
	th := newThrottle(func() time.Time { return now })

	for i := 0; i < 40; i++ {
		if delay := th.fail("203.0.113.7"); delay > loginMaxDelay || delay <= 0 {
			t.Fatalf("задержка %v вне (0, %v]", delay, loginMaxDelay)
		}
	}
}

func TestThrottleSuccessResets(t *testing.T) {
	now := time.Now()
	th := newThrottle(func() time.Time { return now })

	for i := 0; i < maxLoginFails-1; i++ {
		th.fail("203.0.113.7")
	}
	th.success("203.0.113.7")

	for i := 0; i < maxLoginFails-1; i++ {
		th.fail("203.0.113.7")
		if left := th.retryAfter("203.0.113.7"); left != 0 {
			t.Fatalf("счётчик не сброшен успешным входом: блокировка на %d-й неудаче", i+1)
		}
	}
}

// Неудачи забываются сами: иначе одна опечатка в месяц копится до блокировки.
func TestThrottleForgetsOldFails(t *testing.T) {
	now := time.Now()
	th := newThrottle(func() time.Time { return now })

	for i := 0; i < maxLoginFails-1; i++ {
		th.fail("203.0.113.7")
	}
	now = now.Add(failWindow + time.Minute)
	th.fail("203.0.113.7")
	if left := th.retryAfter("203.0.113.7"); left != 0 {
		t.Error("старые неудачи не забыты")
	}
}

func TestThrottleSweep(t *testing.T) {
	now := time.Now()
	th := newThrottle(func() time.Time { return now })

	th.fail("203.0.113.7")
	th.sweep()
	if len(th.entries) != 1 {
		t.Fatalf("свежая запись убрана: %d записей", len(th.entries))
	}

	now = now.Add(failWindow + loginBlock + time.Minute)
	th.sweep()
	if len(th.entries) != 0 {
		t.Errorf("протухшие записи остались: %d", len(th.entries))
	}
}
