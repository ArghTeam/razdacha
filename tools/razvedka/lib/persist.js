/* Отложенная запись сессии в storage.

   На живом сайте запросов сотни в минуту, писать сессию на каждый — расточительно;
   раз в две секунды достаточно, чтобы прогон пережил закрытие DevTools. Таймер
   вынесен сюда и получает «чем сохранять» и «чем отсчитывать» снаружи: так его
   поведение проверяется в test.js без chrome и без ожидания. */

export const PERSIST_DELAY_MS = 2000;

/** Очередь отложенной записи. Пока таймер висит, повторные `schedule()` бесплатны:
    по срабатыванию `save` пишет актуальное состояние целиком, а не снимок момента
    первого вызова — поэтому последняя пачка записей не теряется. */
export function makePersistQueue(save, {
  delay = PERSIST_DELAY_MS,
  setTimer = setTimeout,
  clearTimer = clearTimeout,
} = {}) {
  let timer = null;
  return {
    /** Запланировать запись. `true` — таймер взведён этим вызовом. */
    schedule() {
      if (timer !== null) return false;
      timer = setTimer(() => {
        timer = null;
        save();
      }, delay);
      return true;
    },
    /** Снять висящий таймер — перед финальной записью, чтобы она не задвоилась. */
    cancel() {
      if (timer === null) return false;
      clearTimer(timer);
      timer = null;
      return true;
    },
    get pending() {
      return timer !== null;
    },
  };
}
