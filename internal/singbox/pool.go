package singbox

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Параметры группы `urltest` туннеля-пула (ADR 0010).
const (
	// PoolTestInterval — как часто sing-box перепроверяет участников пула.
	// Ротация внутри группы бесплатна: конфиг на диске не меняется, процесс не
	// перезапускается, соседние туннели не задеты. Три минуты — компромисс между
	// временем жизни мёртвого сервера в работе и постоянным фоновым трафиком.
	PoolTestInterval = 3 * time.Minute

	// poolIdleTimeout — через сколько простоя группа перестаёт проверяться.
	// Значение sing-box по умолчанию; задано явно, потому что интервал проверки
	// обязан быть меньше него.
	poolIdleTimeout = 30 * time.Minute

	// poolMaxServers — сколько серверов пула попадает в конфиг: первые столько в
	// порядке приоритета, который ведёт слой lists. Полнота не нужна:
	// работоспособность даёт горстка ключей, а каждый лишний участник — запись в
	// outbounds[] и проба каждые PoolTestInterval. Шестнадцать переживают отказ
	// большинства и оставляют конфиг читаемым руками.
	poolMaxServers = 16

	// poolTolerance — насколько новый лидер должен быть быстрее текущего (мс),
	// чтобы группа переключилась. Без запаса группа скакала бы на шуме измерений.
	poolTolerance = 50

	// poolTestURL — что запрашивается при проверке. Тот же адрес, что у sing-box
	// по умолчанию; задан явно, чтобы конфиг не менялся при смене его дефолта.
	poolTestURL = "https://www.gstatic.com/generate_204"
)

// unknownAddr — что пишется в лог вместо адреса, когда достать его из ссылки
// нечем. Пустое значение выглядело бы как «адреса нет», а не «не разобрали».
const unknownAddr = "неизвестен"

// serverAddr достаёт из ссылки на сервер `хост:порт` для лога.
//
// Сама ссылка в лог не попадает никогда: в ней UUID или пароль, а журнал демона
// читают шире, чем БД (issue #124).
//
// Разбор ручной, а не через [url.Parse]: сюда попадают в том числе ссылки,
// которые парсер уже не осилил, а `url.Parse` спотыкается о не-ASCII в
// логин-части (`trojan://пароль@…`) и отдал бы «адрес неизвестен» там, где он
// есть. Хост принимается только если выглядит адресом ([plainHost]): у `ss://`
// на его месте бывает base64 со всем содержимым ключа.
func serverAddr(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// Логин отбрасывается по последнему `@`: пароль сам по себе может его содержать.
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}

	host, port := s, ""
	if h, p, err := net.SplitHostPort(s); err == nil {
		host, port = h, p
	}
	host = strings.Trim(host, "[]")
	if !plainHost(host) {
		return unknownAddr
	}
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	return host
}

// plainHost — похоже ли это на имя хоста или IP, а не на закодированный ключ.
// Base64 живёт в алфавите без точек, поэтому обязательная точка отсекает его, а
// вместе с ним и всё, что не является адресом.
func plainHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '.', r == '_':
		default:
			return false
		}
	}
	return strings.Contains(h, ".")
}

// poolMemberTag — тег одного участника пула. Индекс — позиция в отобранном наборе,
// а порядок отбора держит слой lists, поэтому уцелевший сервер сохраняет свой тег, и
// неизменившийся набор даёт тот же JSON.
func poolMemberTag(tunnelID string, i int) string {
	return fmt.Sprintf("%s-%d", TunnelTag(tunnelID), i)
}

// PoolMembers отдаёт участников группы пула: тег в конфиге и сервер, которому он
// соответствует. Порядок тот же, что в конфиге.
//
// Нужен слою api: Clash API отдаёт выбранный участник тегом, а показать
// пользователю надо имя и страну сервера, и вычислять отбор второй раз своими
// руками означало бы разойтись с генератором.
func PoolMembers(t store.Tunnel) map[string]store.PoolServer {
	servers := selectPoolServers(t.Pool)
	out := make(map[string]store.PoolServer, len(servers))
	for i, s := range servers {
		out[poolMemberTag(t.ID, i)] = s
	}
	return out
}

// buildPool разворачивает туннель-пул в участников группы и сам `urltest`.
//
// Тег группы — TunnelTag(t.ID), то есть ровно тот, на который ссылаются правила:
// снаружи пул остаётся одним туннелем (ADR 0010). Второе значение — false, если в
// конфиг не попал ни один участник; тогда пул выпадает из конфига, как выключенный
// туннель, а ссылающиеся на него правила остаются отказом (ADR 0013). Ошибка здесь
// заморозила бы весь конфиг, включая исправные туннели, из-за чужого недоступного
// каталога.
func buildPool(t store.Tunnel, log *slog.Logger) ([]option.Outbound, bool) {
	servers := selectPoolServers(t.Pool)
	out := make([]option.Outbound, 0, len(servers)+1)
	tags := make([]string, 0, len(servers))
	insecure := 0

	for i, s := range servers {
		res, err := Parse(s.URL)
		if err != nil {
			log.Warn("сервер пула пропущен: ссылка не разобрана",
				"туннель", t.Name, "сервер", s.Title, "адрес", serverAddr(s.URL), "ошибка", err)
			continue
		}
		if res.Outbound == nil {
			log.Warn("сервер пула пропущен: не outbound",
				"туннель", t.Name, "сервер", s.Title, "адрес", serverAddr(s.URL), "тип", res.Type)
			continue
		}
		ob := *res.Outbound
		if forceCertVerify(&ob) {
			insecure++
		}
		// Тег привязан к позиции в наборе, а не к номеру принятого сервера:
		// иначе один неразобравшийся ключ сдвинул бы теги всех следующих.
		ob.Tag = poolMemberTag(t.ID, i)
		out = append(out, ob)
		tags = append(tags, ob.Tag)
	}

	// Одной строкой на пул, а не на сервер: каталог раздаёт `allowInsecure=1`
	// пачками, и построчный лог утонул бы в повторах на каждой генерации конфига.
	if insecure > 0 {
		log.Warn("серверы пула просили не проверять сертификат — проверка оставлена включённой",
			"туннель", t.Name, "серверов", insecure, "всего", len(tags))
	}

	if len(tags) == 0 {
		log.Warn("туннель-пул пропущен: нет ни одного пригодного сервера",
			"туннель", t.Name, "серверов_в_бд", len(t.Pool))
		return nil, false
	}

	return append(out, option.Outbound{
		Type: C.TypeURLTest,
		Tag:  TunnelTag(t.ID),
		Options: &option.URLTestOutboundOptions{
			Outbounds:   tags,
			URL:         poolTestURL,
			Interval:    badoption.Duration(PoolTestInterval),
			Tolerance:   poolTolerance,
			IdleTimeout: badoption.Duration(poolIdleTimeout),
		},
	}), true
}

// forceCertVerify снимает у сервера пула «не проверять сертификат» и говорит,
// был ли флаг поднят.
//
// Флаг `allowInsecure` (он же `insecure`, он же `skip-cert-verify` у других
// клиентов) в ссылке, которую вставил человек, — его собственное решение: свой
// сервер с самоподписанным сертификатом имеет право работать. Ключ пула никто не
// вставлял: демон взял его сам со страницы, которую мы не контролируем (ADR 0010),
// и одна строка в чужой выдаче сняла бы шифрование в том самом канале, ради
// которого туннель существует. Согласие на перехват должно быть осознанным, а
// здесь его дать некому.
//
// Гасится здесь, а не в [proxyURL.insecure]: разбор ссылки одинаков для всех
// источников и обязан таким остаться — тот же `Parse` обслуживает ручную вставку,
// где флаг законен, и ручку проверки ключа в панели. Источник известен ровно на
// этом уровне, поэтому источникозависимая правка живёт тут. Не переносить обратно
// в parse_url.go как «пропущенный случай» (issue #128).
func forceCertVerify(ob *option.Outbound) bool {
	w, ok := ob.Options.(option.OutboundTLSOptionsWrapper)
	if !ok {
		return false
	}
	tls := w.TakeOutboundTLSOptions()
	if tls == nil || !tls.Insecure {
		return false
	}
	// Копия, а не правка на месте: секция TLS общая с результатом разбора, и
	// портить чужую структуру ради своей копии outbound'а незачем.
	fixed := *tls
	fixed.Insecure = false
	w.ReplaceOutboundTLSOptions(&fixed)
	return true
}

// selectPoolServers отбирает серверы для конфига: первые poolMaxServers штук в том
// порядке, в каком они лежат в БД, без пустых ссылок и повторов.
//
// Порядок в БД — это и есть приоритет отбора, а не случайность записи: его ведёт
// слой lists при обходе каталога (`lists.MergePool`). Известный сервер держит своё
// место, пока он есть в каталоге, новые встают на освободившиеся места лучшими по
// пингу впереди. Пересортировка здесь по пингу из карточки была бы ошибкой: пинг
// пляшет на каждом обходе, шестнадцать участников менялись бы вместе с ним, а с ними
// и байты конфига. А байты решают всё: `sameOnDisk` в apply.go сравнивает конфиг
// байт в байт, и любое расхождение идёт в `systemctl reload-or-restart`, то есть в
// перезапуск sing-box с разрывом соединений во всех туннелях (ADR 0010, issue #68).
func selectPoolServers(pool []store.PoolServer) []store.PoolServer {
	seen := make(map[string]bool, len(pool))
	uniq := make([]store.PoolServer, 0, len(pool))
	for _, s := range pool {
		if s.URL == "" || seen[s.URL] {
			continue
		}
		seen[s.URL] = true
		uniq = append(uniq, s)
		if len(uniq) == poolMaxServers {
			break
		}
	}
	return uniq
}
