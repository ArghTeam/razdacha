/* Регистрация панели в DevTools.

   Больше этот файл ничего не делает: вся работа — в panel.js, который живёт в
   контексте самой панели и только там имеет доступ к
   `chrome.devtools.network.onRequestFinished`.

   Не модуль: страница devtools_page грузится обычным <script>, и type="module"
   здесь ничего не даёт. */

chrome.devtools.panels.create(
  'razvedka',
  '',
  'panel.html',
  () => {},
);
