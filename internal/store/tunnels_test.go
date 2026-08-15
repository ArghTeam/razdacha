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

// catalogURL — каталог ключей, с которым заводится встроенный пул. Туннель-пул в
// обход [Store.EnsureBuiltinPool] заводит samplePool: так выглядит установка, где пул
// завели руками, пока это было можно.
const catalogURL = "https://outlinekeys.com/protocols/outline/"

// deadCatalogURL — каталог, которого больше нет. Так выглядит адрес пула у всех
// установок, поставленных до ADR 0015.
const deadCatalogURL = "https://vpnkeys.me/protocol/vless"

// Встроенный пул заводится один раз: повторный вызов (то есть повторный старт
// демона) отдаёт ту же запись, а не заводит вторую.
func TestEnsureBuiltinPoolIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	first, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	if !first.Created {
		t.Fatal("первый вызов не создал встроенный пул")
	}
	if !first.Tunnel.Builtin || first.Tunnel.Source != SourcePool || first.Tunnel.Type != TunnelShadowsocks {
		t.Errorf("встроенный пул заведён как %+v", first.Tunnel)
	}
	// Выключенным: свежая установка не должна сама пойти на чужой сайт за ключами.
	if first.Tunnel.Enabled {
		t.Error("встроенный пул заведён включённым")
	}

	second, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("повторный EnsureBuiltinPool: %v", err)
	}
	if second.Created || second.Adopted {
		t.Errorf("повторный вызов завёл вторую запись: %+v", second)
	}
	if second.Tunnel.ID != first.Tunnel.ID {
		t.Errorf("повторный вызов отдал другой пул: %s вместо %s", second.Tunnel.ID, first.Tunnel.ID)
	}
	if second.OtherPools != 0 {
		t.Errorf("помимо встроенного насчитано %d пулов", second.OtherPools)
	}

	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("туннелей в БД %d, ожидался один", len(list))
	}
}

// Признак встроенного держится на колонке, а не на имени и каталоге: переименованный
// и переключённый на другой каталог пул остаётся тем же встроенным.
func TestEnsureBuiltinPoolSurvivesRename(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	first, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}

	tun := first.Tunnel
	tun.Name = "Мой пул"
	tun.Raw = "https://example.org/keys"
	tun.Enabled = true
	if err := s.UpdateTunnel(ctx, tun); err != nil {
		t.Fatalf("UpdateTunnel: %v", err)
	}

	got, err := s.Tunnel(ctx, tun.ID)
	if err != nil {
		t.Fatalf("Tunnel: %v", err)
	}
	if !got.Builtin {
		t.Error("правка сняла признак встроенного — запрет на удаление обходится через PATCH")
	}

	same, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("повторный EnsureBuiltinPool: %v", err)
	}
	if same.Created || same.Tunnel.ID != tun.ID {
		t.Errorf("после переименования завёлся второй пул: %+v", same)
	}
}

// Пул, заведённый руками до того, как пулы стали встроенными, признаётся встроенным,
// а не дублируется вторым: пул в системе один.
func TestEnsureBuiltinPoolAdoptsExisting(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	existing, err := s.CreateTunnel(ctx, samplePool("Бесплатные VLESS (встроенный)"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	res, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	if res.Created {
		t.Fatal("рядом с существующим пулом заведён второй")
	}
	if !res.Adopted || res.Tunnel.ID != existing.ID || !res.Tunnel.Builtin {
		t.Fatalf("существующий пул не признан встроенным: %+v", res)
	}
	// Имя не трогается: на работающей установке пул уже называется как называется.
	if res.Tunnel.Name != existing.Name {
		t.Errorf("пул переименован в %q", res.Tunnel.Name)
	}
	// Включённость тоже: выключать работающий пул при обновлении незачем.
	if !res.Tunnel.Enabled {
		t.Error("работающий пул выключен при признании встроенным")
	}

	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("туннелей в БД %d, ожидался один", len(list))
	}
}

// Пулов в БД несколько (ручная правка, старая установка): встроенным становится один
// и тот же — самый старый, — а остальные остаются обычными туннелями. Их число
// возвращается наружу, чтобы демону было о чём написать в лог.
func TestEnsureBuiltinPoolPicksOneOfMany(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	older := samplePool("Первый пул")
	older.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := s.CreateTunnel(ctx, older)
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	newer := samplePool("Второй пул")
	newer.CreatedAt = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.CreateTunnel(ctx, newer); err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	res, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	if !res.Adopted || res.Tunnel.ID != first.ID {
		t.Fatalf("встроенным стал не самый старый пул: %+v", res)
	}
	if res.OtherPools != 1 {
		t.Errorf("остальных пулов насчитано %d, ожидался один", res.OtherPools)
	}

	// Выбор одинаков на каждом старте: иначе признак перескакивал бы с записи на запись.
	again, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("повторный EnsureBuiltinPool: %v", err)
	}
	if again.Tunnel.ID != first.ID || again.Created || again.Adopted {
		t.Errorf("повторный вызов выбрал другой пул: %+v", again)
	}

	// Остальные не тронуты: они работают как работали.
	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	builtin := 0
	for _, tun := range list {
		if tun.Builtin {
			builtin++
		}
	}
	if len(list) != 2 || builtin != 1 {
		t.Fatalf("туннелей %d, встроенных среди них %d", len(list), builtin)
	}
}

// Имя занято туннелем пользователя, и это не пул: пул не заводится, но отказ внятный —
// демон пишет его в лог, а установка остаётся работать без пула.
func TestEnsureBuiltinPoolReportsTakenName(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.CreateTunnel(ctx, sampleTunnel("Бесплатные ключи")); err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	_, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
	if !strings.Contains(err.Error(), "Бесплатные ключи") {
		t.Errorf("ошибка не называет занятое имя: %v", err)
	}
}

// Встроенную запись не удаляют, а выключают: на следующем старте демон завёл бы её
// снова, и «удалил» оказалось бы неправдой.
func TestDeleteBuiltinPoolIsRejected(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	res, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", catalogURL, TunnelShadowsocks)
	if err != nil {
		t.Fatalf("EnsureBuiltinPool: %v", err)
	}
	pool := res.Tunnel

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

// Пул с умершим каталогом переезжает на живой: адрес, протокол и — обязательно —
// пустой состав. Ключи закрывшегося сайта чужого протокола в конфиг попасть не
// должны (issue #153).
func TestRetargetBuiltinPool(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	res, err := s.EnsureBuiltinPool(ctx, "Бесплатные ключи", deadCatalogURL, TunnelVLESS)
	if err != nil || !res.Created {
		t.Fatalf("EnsureBuiltinPool: %v (%+v)", err, res)
	}
	servers := []PoolServer{{URL: "vless://a@1.2.3.4:443", Country: "Германия"}}
	if err := s.UpdateTunnelPool(ctx, res.Tunnel.ID, servers, time.Now().UTC()); err != nil {
		t.Fatalf("запись состава: %v", err)
	}

	if err := s.RetargetBuiltinPool(ctx, res.Tunnel.ID, catalogURL, TunnelShadowsocks); err != nil {
		t.Fatalf("RetargetBuiltinPool: %v", err)
	}

	got, err := s.Tunnel(ctx, res.Tunnel.ID)
	if err != nil {
		t.Fatalf("чтение пула: %v", err)
	}
	if got.Raw != catalogURL || got.Type != TunnelShadowsocks {
		t.Errorf("пул остался на прежнем каталоге: raw=%q type=%q", got.Raw, got.Type)
	}
	if len(got.Pool) != 0 || !got.PoolUpdatedAt.IsZero() {
		t.Errorf("состав умершего каталога уцелел: %d серверов, обход %v",
			len(got.Pool), got.PoolUpdatedAt)
	}
}

// Пул, заведённый человеком, переезда не получает: каталог в нём — его решение.
func TestRetargetSkipsUserPool(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	own, err := s.CreateTunnel(ctx, samplePool("свой пул"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	err = s.RetargetBuiltinPool(ctx, own.ID, catalogURL, TunnelShadowsocks)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено: %v", err)
	}
}
