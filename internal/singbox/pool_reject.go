package singbox

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

// initIndexRe вытаскивает из ошибки `sing-box check` вид сущности и её номер:
// `initialize outbound[N]` либо `initialize endpoint[N]`. Формат задаёт сам
// рантайм (sing-box box.go, E.Cause); тег в это сообщение не печатается, поэтому
// номер — единственное, по чему узнаётся виноватый. Первое совпадение слева — это
// внешняя причина, то есть та самая сущность, что не собралась (ADR 0019).
var initIndexRe = regexp.MustCompile(`initialize (outbound|endpoint)\[(\d+)\]`)

// rejectedMember — участник пула, которого рантайм отверг: чем он был и откуда
// взялся, чтобы выкинуть ровно его и записать в лог адрес без ключа (ADR 0019).
type rejectedMember struct {
	tunnelID   string
	tunnelName string
	url        string
	addr       string
}

// rejectedPoolMember отвечает, споткнулся ли `sing-box check` именно об участника
// пула. Терпимость строго к членам пула (ADR 0019): ручной туннель, второе звено
// цепи и любой не-pool outbound под неё не попадают — их отказ остаётся отказом
// всего применения (ADR 0013 — про доступность, здесь про негодность).
//
// Виноватого рантайм называет номером в outbounds[]/endpoints[]. Номер переводится
// в тег по самому конфигу, тег — в сервер по снимку. Эндпоинтом участник пула не
// бывает (пул всегда outbound), поэтому endpoint[N] членом пула не считается.
func rejectedPoolMember(opts option.Options, snap store.Snapshot, checkErr error) (rejectedMember, bool) {
	m := initIndexRe.FindStringSubmatch(checkErr.Error())
	if m == nil || m[1] != "outbound" {
		return rejectedMember{}, false
	}
	idx, err := strconv.Atoi(m[2])
	if err != nil || idx < 0 || idx >= len(opts.Outbounds) {
		return rejectedMember{}, false
	}
	member, ok := poolMembersByTag(snap)[opts.Outbounds[idx].Tag]
	return member, ok
}

// poolMembersByTag сопоставляет тег участника в конфиге с сервером, за которым он
// стоит. Строится тем же отбором `selectPoolServers` и той же схемой тегов
// `poolMemberTag`, что и генератор: иначе номер из ошибки указал бы не на того.
func poolMembersByTag(snap store.Snapshot) map[string]rejectedMember {
	out := make(map[string]rejectedMember)
	filter := store.PoolFilterFrom(snap.Settings)
	for _, t := range snap.Tunnels {
		if !t.Enabled || t.Source != store.SourcePool {
			continue
		}
		for i, s := range selectPoolServers(t.Pool, filter) {
			out[poolMemberTag(t.ID, i)] = rejectedMember{
				tunnelID:   t.ID,
				tunnelName: t.Name,
				url:        s.URL,
				addr:       serverAddr(s.URL),
			}
		}
	}
	return out
}

// withoutPoolServer возвращает копию снимка, из пула указанного туннеля вычеркнут
// сервер с этой ссылкой. Копируется только затронутый туннель и его срез Pool:
// снимок вызывающего общий, портить его нельзя. Убирается из всего пула, а не
// только из окна конфига, поэтому пересборка доберёт окно годными из остатка до
// потолка (ADR 0019).
func withoutPoolServer(snap store.Snapshot, tunnelID, url string) store.Snapshot {
	tunnels := make([]store.Tunnel, len(snap.Tunnels))
	copy(tunnels, snap.Tunnels)
	for i := range tunnels {
		if tunnels[i].ID != tunnelID {
			continue
		}
		pool := make([]store.PoolServer, 0, len(tunnels[i].Pool))
		for _, s := range tunnels[i].Pool {
			if s.URL != url {
				pool = append(pool, s)
			}
		}
		tunnels[i].Pool = pool
		break
	}
	snap.Tunnels = tunnels
	return snap
}

// poolServerCount — сколько всего серверов лежит в пулах снимка. Потолок числа
// выкидываний: отвергнуть можно не больше, чем есть серверов, а цикл обязан
// заканчиваться (ADR 0019).
func poolServerCount(snap store.Snapshot) int {
	n := 0
	for _, t := range snap.Tunnels {
		if t.Source == store.SourcePool {
			n += len(t.Pool)
		}
	}
	return n
}

// rejectReason сжимает ошибку рантайма до первой строки для лога: причина отказа
// нужна, а простыня из вложенных обёрток — нет. Ключ в сообщении `sing-box` не
// печатается (там протокольная причина, не ссылка), адрес пишется отдельно.
func rejectReason(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
