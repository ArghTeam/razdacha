package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// warpRegistrar — то, что этому файлу нужно от регистрации WARP. Интерфейс, а не
// *singbox.WARPRegistrar: в тестах настоящий api.cloudflareclient.com не участвует.
type warpRegistrar interface {
	Register(ctx context.Context) (singbox.WARPDevice, error)
}

// defaultWARPName — имя туннеля, который заводит кнопка. Пользователь может
// прислать своё; переименовать потом можно как у любого туннеля.
const defaultWARPName = "WARP"

// handleAddWARP — `POST /api/tunnels/warp`.
//
// Единственное место, откуда демон ходит в Cloudflare: ни старт, ни миграция, ни
// расписание туда не заглядывают — тот же контракт, что у туннеля-пула (ADR 0010).
//
// Конфиг здесь не применяется: как и у остальных правок туннелей, применение —
// отдельный `POST /api/apply`.
func (s *Server) handleAddWARP(w http.ResponseWriter, r *http.Request) {
	var req tunnelRequest
	if r.ContentLength != 0 && !s.decodeBody(w, r, &req) {
		return
	}

	// Кнопка второй раз не срабатывает. Это ограничение кнопки, а не модели:
	// несколько туннелей с `source = warp` законны и заводятся вставкой `.conf`
	// через `POST /api/tunnels`, цепочкам ADR 0012 они не мешают. Бережём мы
	// Cloudflare: каждая регистрация — реальное устройство на их стороне, и
	// повторное нажатие оставляло бы лишние. Отсюда и проверка до запроса наружу.
	list, err := s.store.Tunnels(r.Context())
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	for _, t := range list {
		if t.Source == store.SourceWARP {
			writeError(w, s.log, http.StatusConflict, codeConflict,
				"WARP уже заведён — туннель «"+t.Name+"». Кнопка второй раз не "+
					"регистрирует устройство у Cloudflare; ещё один WARP можно "+
					"добавить, вставив его .conf вручную")
			return
		}
	}

	dev, err := s.warpRegistrar().Register(r.Context())
	if err != nil {
		s.warpError(w, err)
		return
	}
	s.log.Info("зарегистрировано устройство WARP", "устройство", dev.DeviceID)

	res, perr := singbox.Parse(dev.Conf)
	if perr != nil || res.Source != store.SourceWARP {
		// Конфиг собрали мы сами: если он не разбирается, сломан демон, а не ввод.
		s.log.Error("конфиг WARP не разобран", "ошибка", perr, "форма", res.Source)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal,
			"Cloudflare выдал ключ, но собрать из него туннель не удалось")
		return
	}

	name := defaultWARPName
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		name = strings.TrimSpace(*req.Name)
	}

	created, err := s.store.CreateTunnel(r.Context(), store.Tunnel{
		Name:      name,
		Type:      res.Type,
		Source:    res.Source,
		Raw:       dev.Conf,
		Parsed:    res.Parsed,
		Enabled:   true,
		CreatedAt: s.now().UTC(),
	})
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	writeJSON(w, s.log, http.StatusCreated, newTunnelResponse(created, s.poolInterval()))
}

// warpError переводит отказ регистрации в ответ панели.
//
// Сеть и отказ Cloudflare — разные события с разными действиями пользователя,
// поэтому и тексты разные. Фраза для человека собирается здесь, а не приезжает
// из слоя singbox: там лежит причина ошибки Go, и подробность из неё
// подставляется в готовое предложение ([userMessage] снимает префикс сентинела).
func (s *Server) warpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, singbox.ErrWARPUnreachable):
		s.log.Warn("регистрация WARP: сеть", "ошибка", err)
		writeError(w, s.log, http.StatusBadGateway, codeInternal,
			"Не удалось связаться с Cloudflare — "+userMessage(err, singbox.ErrWARPUnreachable))
	case errors.Is(err, singbox.ErrWARPRejected):
		s.log.Warn("регистрация WARP: отказ", "ошибка", err)
		writeError(w, s.log, http.StatusBadGateway, codeInternal,
			"Cloudflare отказал в регистрации ("+userMessage(err, singbox.ErrWARPRejected)+
				"). Попробуйте позже.")
	default:
		s.log.Error("регистрация WARP", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
	}
}

// warpRegistrar отдаёт регистратор: настоящий клиент API Cloudflare либо
// подставленный тестом.
func (s *Server) warpRegistrar() warpRegistrar {
	if s.warp != nil {
		return s.warp
	}
	return singbox.NewWARPRegistrar(singbox.WARPOptions{})
}
