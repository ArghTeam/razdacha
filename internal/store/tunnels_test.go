package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func sampleTunnel(name string) Tunnel {
	return Tunnel{
		Name:    name,
		Type:    TunnelVLESS,
		Source:  SourceURL,
		Raw:     "vless://uuid@example.org:443?type=tcp&security=tls#" + name,
		Parsed:  []byte(`{"type":"vless"}`),
		Enabled: true,
	}
}

func TestTunnelRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	created, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatal("CreateTunnel не заполнил id или дату создания")
	}

	got, err := s.Tunnel(ctx, created.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if got.Name != created.Name || got.Type != TunnelVLESS || got.Raw != created.Raw {
		t.Errorf("прочитан не тот туннель: %+v", got)
	}
	if string(got.Parsed) != `{"type":"vless"}` {
		t.Errorf("parsed = %s", got.Parsed)
	}
	if !got.Enabled {
		t.Error("enabled потерялся при чтении")
	}

	got.Name = "запасной"
	got.Enabled = false
	if err := s.UpdateTunnel(ctx, got); err != nil {
		t.Fatalf("UpdateTunnel: %v", err)
	}
	after, err := s.Tunnel(ctx, got.ID)
	if err != nil {
		t.Fatalf("Tunnel после обновления: %v", err)
	}
	if after.Name != "запасной" || after.Enabled {
		t.Errorf("обновление не применилось: %+v", after)
	}
	if !after.CreatedAt.Equal(created.CreatedAt.UTC().Truncate(0)) &&
		after.CreatedAt.Unix() != created.CreatedAt.Unix() {
		t.Errorf("дата создания изменилась: %v → %v", created.CreatedAt, after.CreatedAt)
	}

	if err := s.DeleteTunnel(ctx, got.ID); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	if _, err := s.Tunnel(ctx, got.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("после удаления ожидалась ErrNotFound, получено: %v", err)
	}
}

func samplePool(name string) Tunnel {
	return Tunnel{
		Name:    name,
		Type:    TunnelVLESS,
		Source:  SourcePool,
		Raw:     "https://vpnkeys.me/protocol/vless",
		Enabled: true,
	}
}

// Пул проходит полный круг: создаётся без своего конфига, наполняется отдельной
// операцией и читается со временем обновления.
func TestPoolTunnelRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	created, err := s.CreateTunnel(ctx, samplePool("пул vless"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if len(created.Pool) != 0 || !created.PoolUpdatedAt.IsZero() {
		t.Errorf("свежий пул не пуст: %+v", created)
	}

	servers := []PoolServer{
		{URL: "vless://a@1.2.3.4:443?type=tcp", Country: "Нидерланды", Title: "NL-1", PingMS: 42},
		{URL: "vless://b@5.6.7.8:443?type=tcp", Country: "Германия", Title: "DE-1", PingMS: 88},
	}
	at := time.Unix(1750000000, 0).UTC()
	if err := s.UpdateTunnelPool(ctx, created.ID, servers, at); err != nil {
		t.Fatalf("UpdateTunnelPool: %v", err)
	}

	got, err := s.Tunnel(ctx, created.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if len(got.Pool) != 2 || got.Pool[0].URL != servers[0].URL || got.Pool[1].PingMS != 88 {
		t.Errorf("серверы пула прочитаны не так, как записаны: %+v", got.Pool)
	}
	if !got.PoolUpdatedAt.Equal(at) {
		t.Errorf("время обновления пула = %v, ожидалось %v", got.PoolUpdatedAt, at)
	}

	// Обновление туннеля целиком состав пула не теряет.
	got.Name = "пул"
	if err := s.UpdateTunnel(ctx, got); err != nil {
		t.Fatalf("UpdateTunnel: %v", err)
	}
	after, err := s.Tunnel(ctx, got.ID)
	if err != nil {
		t.Fatalf("Tunnel после обновления: %v", err)
	}
	if len(after.Pool) != 2 || !after.PoolUpdatedAt.Equal(at) {
		t.Errorf("обновление туннеля потеряло пул: %+v", after)
	}
}

// Состав пула пишется только пулу: обычный туннель такой операцией не задеть.
func TestUpdateTunnelPoolOnlyForPools(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	plain, err := s.CreateTunnel(ctx, sampleTunnel("обычный"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	err = s.UpdateTunnelPool(ctx, plain.ID, []PoolServer{{URL: "vless://a@1.2.3.4:443"}}, time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}

func TestPoolTunnelValidation(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	cases := map[string]func(*Tunnel){
		// Протокол пула задаёт драйвер каталога (ADR 0015), и trojan у пула
		// законен; невозможны только endpoint-протокол и «не распознан».
		"пул wireguard":       func(v *Tunnel) { v.Type = TunnelWireGuard },
		"пул без протокола":   func(v *Tunnel) { v.Type = "" },
		"у пула свой конфиг":  func(v *Tunnel) { v.Parsed = []byte(`{"type":"vless"}`) },
		"сервер без ссылки":   func(v *Tunnel) { v.Pool = []PoolServer{{Country: "Германия"}} },
		"пустой URL каталога": func(v *Tunnel) { v.Raw = "" },
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			v := samplePool("пул vless")
			broken(&v)
			if _, err := s.CreateTunnel(ctx, v); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
			}
		})
	}

	// Обратная сторона: серверы у не-пула — тоже ошибка, иначе они молча лежали бы
	// в БД и не попадали в конфиг.
	v := sampleTunnel("обычный")
	v.Pool = []PoolServer{{URL: "vless://a@1.2.3.4:443"}}
	if _, err := s.CreateTunnel(ctx, v); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid на серверы у обычного туннеля, получено: %v", err)
	}
}

func TestTunnelNameIsUnique(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.CreateTunnel(ctx, sampleTunnel("основной")); err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	_, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid на дубль имени, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "основной") {
		t.Errorf("в ошибке нет имени туннеля: %v", err)
	}
}

func TestTunnelValidation(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	cases := map[string]func(*Tunnel){
		"пустое имя":      func(v *Tunnel) { v.Name = "" },
		"неизвестный тип": func(v *Tunnel) { v.Type = "wireguad" },
		"пустой конфиг":   func(v *Tunnel) { v.Raw = "" },
		"parsed не JSON":  func(v *Tunnel) { v.Parsed = []byte("{") },
	}
	for name, broken := range cases {
		t.Run(name, func(t *testing.T) {
			v := sampleTunnel("основной")
			broken(&v)
			if _, err := s.CreateTunnel(ctx, v); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
			}
		})
	}
}

// Удаление туннеля, на который ссылается правило, отклоняется внятной ошибкой,
// а не тихо ломает маршрутизацию.
func TestDeleteTunnelInUseIsRejected(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if _, err := s.CreateRule(ctx, sampleRule("YouTube и Google", tunnel.ID)); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	err = s.DeleteTunnel(ctx, tunnel.ID)
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("ожидалась ErrInUse, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "YouTube и Google") {
		t.Errorf("в ошибке не названо правило: %v", err)
	}
	if _, err := s.Tunnel(ctx, tunnel.ID); err != nil {
		t.Errorf("туннель всё-таки удалён: %v", err)
	}
}

// Та же защита уровнем ниже: ON DELETE RESTRICT не даёт удалить туннель в обход слоя.
func TestDeleteTunnelRestrictedBySchema(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	tunnel, err := s.CreateTunnel(ctx, sampleTunnel("основной"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if _, err := s.CreateRule(ctx, sampleRule("Соцсети", tunnel.ID)); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM tunnels WHERE id = ?`, tunnel.ID); err == nil {
		t.Fatal("прямое удаление прошло — ON DELETE RESTRICT не работает")
	}
}

func TestDeleteUnknownTunnel(t *testing.T) {
	s := open(t)
	if err := s.DeleteTunnel(context.Background(), "нет-такого"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}

// catalogURL — каталог ключей встроенного общего пула (ADR 0018).
const catalogURL = "https://raw.githack.com/igareck/vpn-configs-for-russia/main/"

// builtinCountryPool заводит встроенный страновой пул — так выглядит установка,
// поставленная на версии со странами (ADR 0017). Через [Store.EnsureBuiltinPool]
// такой уже не появляется, но у обновляющихся их семь, и сворачивание в один надо
// проверять. createdAt задаётся явно: [collapseBuiltinPools] оставляет самый ранний,
// и порядок должен быть детерминирован.
func builtinCountryPool(t *testing.T, s *Store, name, country string, createdAt time.Time) Tunnel {
	t.Helper()
	tun, err := s.CreateTunnel(context.Background(), Tunnel{
		Name:      name,
		Type:      TunnelVLESS,
		Source:    SourcePool,
		Raw:       catalogURL,
		Country:   country,
		Enabled:   true,
		Builtin:   true,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("заведение странового встроенного пула %q: %v", name, err)
	}
	return tun
}

// theBuiltinPool находит единственный встроенный пул в списке.
func theBuiltinPool(t *testing.T, list []Tunnel) Tunnel {
	t.Helper()
	var found []Tunnel
	for _, tn := range list {
		if tn.Builtin && tn.Source == SourcePool {
			found = append(found, tn)
		}
	}
	if len(found) != 1 {
		t.Fatalf("встроенных пулов %d, ожидался один: %+v", len(found), found)
	}
	return found[0]
}

// Свежая БД: заводится ровно один встроенный пул — выключенный, встроенный,
// source=pool, с именем [builtinPoolName] и общим каталогом. Повторный вызов ничего
// не плодит.
func TestEnsureBuiltinPoolSeedsOne(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	res, err := s.EnsureBuiltinPool(ctx, catalogURL, TunnelVLESS)
	if err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	if !res.Created {
		t.Fatalf("на свежей БД пул не заведён: %+v", res)
	}
	if len(res.RemovedExtra) != 0 || len(res.DetachedRules) != 0 {
		t.Errorf("на свежей БД что-то свёрнуто/отвязано: %+v", res)
	}

	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	pool := theBuiltinPool(t, list)
	if pool.Enabled {
		t.Errorf("пул заведён включённым: %+v", pool)
	}
	if pool.Type != TunnelVLESS || pool.Raw != catalogURL || pool.Country != "" {
		t.Errorf("пул заведён с неверными полями: %+v", pool)
	}
	if pool.Name != builtinPoolName {
		t.Errorf("имя пула %q, ожидалось %q", pool.Name, builtinPoolName)
	}

	// Идемпотентность: второй старт ничего не заводит, число не растёт.
	again, err := s.EnsureBuiltinPool(ctx, catalogURL, TunnelVLESS)
	if err != nil {
		t.Fatalf("повторный EnsureBuiltinPool: %v", err)
	}
	if again.Created || len(again.RemovedExtra) != 0 {
		t.Errorf("повторный вызов что-то менял: %+v", again)
	}
	list, err = s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	theBuiltinPool(t, list) // всё ещё ровно один
}

// Признак держится на флаге builtin, а не на имени: переименованный пользователем
// пул на следующем старте не плодит второй.
func TestEnsureBuiltinPoolSurvivesRename(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.EnsureBuiltinPool(ctx, catalogURL, TunnelVLESS); err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	pool := theBuiltinPool(t, list)
	pool.Name = "Мой любимый пул"
	pool.Enabled = true
	if err := s.UpdateTunnel(ctx, pool); err != nil {
		t.Fatalf("UpdateTunnel: %v", err)
	}

	if _, err := s.EnsureBuiltinPool(ctx, catalogURL, TunnelVLESS); err != nil {
		t.Fatalf("повторный EnsureBuiltinPool: %v", err)
	}
	list, err = s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	got := theBuiltinPool(t, list)
	if got.ID != pool.ID || got.Name != "Мой любимый пул" {
		t.Errorf("пул подменён или переименован обратно: %+v", got)
	}
	if !got.Builtin {
		t.Errorf("правка сбила признак встроенного: %+v", got)
	}
}

// Апгрейд с версии со странами: семь встроенных пулов сворачиваются в один. Выживает
// самый ранний, лишние удаляются, а ссылавшиеся правила отвязываются по ADR 0013 —
// первое звено с выключением правила, второе звено цепи с сохранением. Всё видно в
// результате, чтобы демон написал об этом в лог.
func TestEnsureBuiltinPoolCollapsesCountryPools(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	base := time.Now().Add(-time.Hour)
	survivor := builtinCountryPool(t, s, "🇳🇱 Нидерланды", "NL", base)
	extra := builtinCountryPool(t, s, "🇩🇪 Германия", "DE", base.Add(time.Minute))

	// Правило, у которого лишний пул — первое (основное) звено.
	primary, err := s.CreateRule(ctx, sampleRule("В пул напрямую", extra.ID))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	// Правило, у которого лишний пул — второе звено цепи, а первое — обычный туннель.
	head, err := s.CreateTunnel(ctx, sampleTunnel("голова цепи"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	chained := sampleRule("Через цепь", head.ID)
	chained.ViaTunnelID = extra.ID
	chained, err = s.CreateRule(ctx, chained)
	if err != nil {
		t.Fatalf("CreateRule (цепь): %v", err)
	}

	res, err := s.EnsureBuiltinPool(ctx, catalogURL, TunnelVLESS)
	if err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	if res.Created {
		t.Errorf("при сворачивании пул не заводится заново: %+v", res)
	}
	if len(res.RemovedExtra) != 1 || res.RemovedExtra[0] != extra.Name {
		t.Fatalf("лишний пул не свёрнут из результата: %+v", res.RemovedExtra)
	}
	if len(res.DetachedRules) != 2 {
		t.Fatalf("отвязано правил %d, ожидалось 2: %+v", len(res.DetachedRules), res.DetachedRules)
	}

	// Лишнего пула в БД нет, выживший остался и переименован в общий, страна очищена.
	if _, err := s.Tunnel(ctx, extra.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("лишний пул уцелел: %v", err)
	}
	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	got := theBuiltinPool(t, list)
	if got.ID != survivor.ID {
		t.Errorf("выжил не самый ранний пул: %+v", got)
	}
	if got.Name != builtinPoolName || got.Country != "" {
		t.Errorf("выживший пул не нормализован: %+v", got)
	}

	// Правило первого звена отвязано и выключено, иначе генератор упал бы на пустом туннеле.
	gotPrimary, err := s.Rule(ctx, primary.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if gotPrimary.TunnelID != "" || gotPrimary.Enabled {
		t.Errorf("правило первого звена не отвязано/не выключено: %+v", gotPrimary)
	}

	// Правило цепи потеряло второе звено, но осталось включённым и маршрутизирует первым.
	gotChained, err := s.Rule(ctx, chained.ID)
	if err != nil {
		t.Fatalf("Rule: %v", err)
	}
	if gotChained.ViaTunnelID != "" {
		t.Errorf("второе звено цепи не отвязано: %+v", gotChained)
	}
	if !gotChained.Enabled || gotChained.TunnelID != head.ID {
		t.Errorf("правило цепи задето сверх второго звена: %+v", gotChained)
	}

	// Повторный старт сворачивать уже нечего.
	again, err := s.EnsureBuiltinPool(ctx, catalogURL, TunnelVLESS)
	if err != nil {
		t.Fatalf("повторный EnsureBuiltinPool: %v", err)
	}
	if again.Created || len(again.RemovedExtra) != 0 || len(again.DetachedRules) != 0 {
		t.Errorf("повторный вызов снова что-то менял: %+v", again)
	}
}

// Пустой адрес каталога — отказ: пул без источника бесполезен.
func TestEnsureBuiltinPoolRejectsEmptyCatalog(t *testing.T) {
	s := open(t)
	if _, err := s.EnsureBuiltinPool(context.Background(), "", TunnelVLESS); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
}

// Встроенную запись не удаляют, а выключают: на следующем старте демон завёл бы её
// снова, и «удалил» оказалось бы неправдой.
func TestDeleteBuiltinPoolIsRejected(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.EnsureBuiltinPool(ctx, catalogURL, TunnelVLESS); err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	pool := theBuiltinPool(t, list)

	err = s.DeleteTunnel(ctx, pool.ID)
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("ожидалась ErrInUse, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "выключить") {
		t.Errorf("ошибка не объясняет, что делать вместо удаления: %v", err)
	}
	if _, err := s.Tunnel(ctx, pool.ID); err != nil {
		t.Errorf("встроенный пул всё-таки удалён: %v", err)
	}
}

// Флаг встроенного бывает только у пула: у обычного туннеля он означал бы
// неудаляемую запись, которую никто не заводил.
func TestBuiltinOnlyForPool(t *testing.T) {
	s := open(t)
	tun := sampleTunnel("резерв")
	tun.Builtin = true
	if _, err := s.CreateTunnel(context.Background(), tun); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
}

// Страна осмысленна только у пула: у обычного туннеля она означала бы поле без
// применения.
func TestCountryOnlyForPool(t *testing.T) {
	s := open(t)
	tun := sampleTunnel("резерв")
	tun.Country = "NL"
	if _, err := s.CreateTunnel(context.Background(), tun); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
}
