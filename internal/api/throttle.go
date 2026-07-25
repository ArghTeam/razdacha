package api

import (
	"context"
	"sync"
	"time"
)

// Параметры защиты от перебора. Второй слой после `limit_req` в nginx: тот
// режет частоту запросов вслепую, этот знает, какие из них были неудачными.
const (
	// maxLoginFails — сколько неудач подряд с одного адреса до блокировки.
	// Пять: человек, набравший пароль мимо раскладки, в это укладывается.
	maxLoginFails = 5

	// loginBlock — на сколько адрес отключается после серии неудач.
	loginBlock = 15 * time.Minute

	// failWindow — за какое время неудачи «забываются» без успешного входа.
	failWindow = 15 * time.Minute

	// loginBaseDelay и loginMaxDelay — нарастающая задержка ответа на неудачу:
	// удвоение с каждой попыткой, но не дольше максимума, иначе перебор
	// превращается в способ занять все соединения демона.
	loginBaseDelay = 250 * time.Millisecond
	loginMaxDelay  = 4 * time.Second
)

// attempt — состояние одного адреса.
type attempt struct {
	fails        int
	last         time.Time
	blockedUntil time.Time
}

// throttle считает неудачные попытки входа по адресу клиента.
//
// Состояние держится в памяти, а не в БД: перезапуск демона случается редко и
// сбрасывает счётчики, зато вход не пишет в БД на каждую неудачу — иначе сам
// перебор становится нагрузкой на диск.
type throttle struct {
	mu      sync.Mutex
	entries map[string]*attempt
	now     func() time.Time
}

func newThrottle(now func() time.Time) *throttle {
	return &throttle{entries: make(map[string]*attempt), now: now}
}

// retryAfter возвращает, сколько ещё адрес заблокирован. Ноль — можно пробовать.
func (t *throttle) retryAfter(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	e, ok := t.entries[key]
	if !ok {
		return 0
	}
	if left := e.blockedUntil.Sub(t.now()); left > 0 {
		return left
	}
	return 0
}

// fail отмечает неудачную попытку и возвращает задержку перед ответом.
func (t *throttle) fail(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	e, ok := t.entries[key]
	if !ok || now.Sub(e.last) > failWindow {
		e = &attempt{}
		t.entries[key] = e
	}
	e.fails++
	e.last = now
	if e.fails >= maxLoginFails {
		e.blockedUntil = now.Add(loginBlock)
	}

	delay := loginBaseDelay << (e.fails - 1)
	if delay > loginMaxDelay || delay <= 0 {
		delay = loginMaxDelay
	}
	return delay
}

// success сбрасывает счётчик: верный пароль снимает накопленные неудачи.
func (t *throttle) success(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

// sweep выбрасывает записи, которые больше ни на что не влияют. Без уборки
// карта растёт на каждый новый адрес — для публичной панели это утечка.
func (t *throttle) sweep() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	for key, e := range t.entries {
		if now.After(e.blockedUntil) && now.Sub(e.last) > failWindow {
			delete(t.entries, key)
		}
	}
}

// wait выдерживает задержку, прерываясь вместе с запросом: клиент, закрывший
// соединение, не должен держать горутину.
func wait(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
