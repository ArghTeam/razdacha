package api

import (
	"net/http"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

// ListState — что демон знает об обновлении одного списка. Приходит из
// планировщика замыканием ([Config.ListStates]), как заливка nft и содержимое
// plain-списков: слой api про кэш и расписание по-прежнему не знает.
type ListState struct {
	// UpdatedAt — когда источник в последний раз ответил. Нулевое время
	// означает, что удачного обновления не было ни разу.
	UpdatedAt time.Time
	// FailedAt — когда последняя попытка не удалась. Нулевое — не падала.
	FailedAt time.Time
	// Err — причина последней неудачи, на русском и как есть от источника.
	Err string
	// Cached — есть ли разобранное содержимое, пусть и прошлой версии.
	Cached bool
}

// Состояния списка в ответе. Три из них — обязательно разные: список,
// обновившийся успешно, список с ошибкой обновления и список, до которого
// планировщик ещё не дошёл, — это три разные причины, почему правило может не
// ловить домены (issue #149).
const (
	// listUpdated — последняя попытка удалась.
	listUpdated = "updated"
	// listFailed — последняя попытка не удалась; `error` объясняет, чем.
	listFailed = "failed"
	// listNever — источник в наборе, но обновления не было ни разу.
	listNever = "never"
	// listCore — демон этот список не качает: домены ведёт сам sing-box
	// набором типа remote, и состояния обновления у демона нет.
	listCore = "core"
	// listUnknown — источника состояния нет: планировщик не поднялся. Пустоту
	// не заполняем нулём — так и говорим.
	listUnknown = "unknown"
)

// listStatus — состояние источника одного списка правила. Едет вместе с
// правилом (`GET /api/rules`), а не отдельным запросом на каждый список:
// строка правила рисуется целиком за один опрос экрана.
type listStatus struct {
	// Key — ключ community-списка; пусто у своего списка по ссылке.
	Key string `json:"key,omitempty"`
	// URL — адрес своего списка; пусто у community-списка.
	URL string `json:"url,omitempty"`
	// Source — адрес, который качает демон. Пусто означает, что не качает.
	Source string `json:"source,omitempty"`
	// State — одно из listUpdated, listFailed, listNever, listCore, listUnknown.
	State string `json:"state"`
	// SubnetsOnly — демон качает только подсети сервиса, домены ведёт sing-box.
	SubnetsOnly bool `json:"subnets_only,omitempty"`
	// UpdatedAt — время последнего удачного обновления. null означает «не было».
	UpdatedAt *time.Time `json:"updated_at"`
	// Error — причина последней неудачи; показывается пользователю как есть.
	Error string `json:"error,omitempty"`
}

// ruleLists собирает состояние источников всех списков правила в том порядке, в
// каком они рисуются в строке: сперва community, потом свои по ссылке.
func (s *Server) ruleLists(r store.Rule) []listStatus {
	var states map[string]ListState
	known := s.listStates != nil
	if known {
		states = s.listStates()
	}

	out := make([]listStatus, 0, len(r.CommunityLists)+len(r.RemoteLists))
	for _, key := range r.CommunityLists {
		st := listStatus{Key: key}
		url, fetched := lists.CommunitySubnetURL(key)
		switch {
		case !fetched:
			// Подсетей у сервиса нет, .srs с доменами качает сам sing-box —
			// состояния обновления у демона по нему нет и не будет.
			st.State = listCore
		default:
			st.Source = url
			st.SubnetsOnly = true
			fill(&st, states, known)
		}
		out = append(out, st)
	}
	for _, url := range r.RemoteLists {
		st := listStatus{URL: url, Source: url}
		fill(&st, states, known)
		out = append(out, st)
	}
	return out
}

// fill проставляет состояние по записи планировщика.
func fill(st *listStatus, states map[string]ListState, known bool) {
	if !known {
		st.State = listUnknown
		return
	}
	state, ok := states[st.Source]
	if !ok {
		st.State = listNever
		return
	}
	if !state.UpdatedAt.IsZero() {
		u := state.UpdatedAt.UTC()
		st.UpdatedAt = &u
	}
	if state.Err != "" {
		st.State = listFailed
		st.Error = state.Err
		return
	}
	if st.UpdatedAt == nil {
		st.State = listNever
		return
	}
	st.State = listUpdated
}

// handleCommunityLists — `GET /api/lists/community`: каталог готовых списков
// сервисов для формы правила (docs/05-api.md).
//
// Ответ не зависит ни от БД, ни от загруженных списков: каталог описывает
// состав allow-domains, а не состояние демона. Поэтому эндпоинт отвечает и
// тогда, когда планировщик ещё ничего не скачал или скачать не смог — иначе
// при недоступном GitHub в панели было бы нечего выбрать.
func (s *Server) handleCommunityLists(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusOK, lists.Catalog())
}
