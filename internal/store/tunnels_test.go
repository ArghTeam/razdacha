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

// catalogURL — общий каталог ключей встроенных страновых пулов (ADR 0017): один
// адрес на все страны, различает их колонка country.
const catalogURL = "https://raw.githack.com/igareck/vpn-configs-for-russia/main/"

// builtinPool заводит старый единственный встроенный пул без страны — так выглядит
// установка, поставленная до ADR 0017. Через [Store.EnsureBuiltinCountryPools] такой
// уже не появляется, но в БД у обновляющихся он есть, и его удаление надо проверять.
func builtinPool(t *testing.T, s *Store, name string) Tunnel {
	t.Helper()
	tun, err := s.CreateTunnel(context.Background(), Tunnel{
		Name:    name,
		Type:    TunnelShadowsocks,
		Source:  SourcePool,
		Raw:     "https://vpnkeys.me/protocol/vless",
		Enabled: true,
		Builtin: true,
	})
	if err != nil {
		t.Fatalf("заведение старого встроенного пула %q: %v", name, err)
	}
	return tun
}

// countryOf собирает страны заведённых пулов по коду.
func countryOf(list []Tunnel) map[string]Tunnel {
	byCode := make(map[string]Tunnel)
	for _, t := range list {
		if t.Builtin && t.Country != "" {
			byCode[t.Country] = t
		}
	}
	return byCode
}

// Свежая БД: заводится ровно семь страновых пулов — выключенных, встроенных,
// source=pool, с проставленной страной и именем по стране. Повторный вызов ничего
// не плодит.
func TestEnsureBuiltinCountryPoolsSeedsSeven(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	res, err := s.EnsureBuiltinCountryPools(ctx, catalogURL, CountryPools(), TunnelVLESS)
	if err != nil {
		t.Fatalf("EnsureBuiltinCountryPools: %v", err)
	}
	if len(res.Created) != len(CountryPools()) {
		t.Fatalf("заведено стран %d, ожидалось %d", len(res.Created), len(CountryPools()))
	}
	if len(res.RemovedLegacy) != 0 || len(res.DetachedRules) != 0 {
		t.Errorf("на свежей БД что-то удалено/отвязано: %+v", res)
	}

	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(list) != len(CountryPools()) {
		t.Fatalf("туннелей в БД %d, ожидалось %d", len(list), len(CountryPools()))
	}
	byCode := countryOf(list)
	for _, c := range CountryPools() {
		tun, ok := byCode[c.Code]
		if !ok {
			t.Fatalf("нет пула для страны %s", c.Code)
		}
		if tun.Enabled {
			t.Errorf("пул %s заведён включённым", c.Code)
		}
		if !tun.Builtin || tun.Source != SourcePool || tun.Type != TunnelVLESS {
			t.Errorf("пул %s заведён как %+v", c.Code, tun)
		}
		if tun.Raw != catalogURL {
			t.Errorf("у пула %s каталог %q, ожидался общий", c.Code, tun.Raw)
		}
		if tun.Name != c.PoolName() {
			t.Errorf("имя пула %s = %q, ожидалось %q", c.Code, tun.Name, c.PoolName())
		}
	}

	// Идемпотентность: второй старт ничего не заводит, число не растёт.
	again, err := s.EnsureBuiltinCountryPools(ctx, catalogURL, CountryPools(), TunnelVLESS)
	if err != nil {
		t.Fatalf("повторный EnsureBuiltinCountryPools: %v", err)
	}
	if len(again.Created) != 0 {
		t.Errorf("повторный вызов завёл ещё %d пулов", len(again.Created))
	}
	list, err = s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(list) != len(CountryPools()) {
		t.Errorf("после повторного вызова туннелей %d", len(list))
	}
}

// Признак держится на колонке country, а не на имени: переименованный пользователем
// пул на следующем старте не плодит второй для той же страны.
func TestEnsureBuiltinCountryPoolsSurvivesRename(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.EnsureBuiltinCountryPools(ctx, catalogURL, CountryPools(), TunnelVLESS); err != nil {
		t.Fatalf("EnsureBuiltinCountryPools: %v", err)
	}
	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	nl := countryOf(list)["NL"]
	nl.Name = "Мой любимый пул"
	nl.Enabled = true
	if err := s.UpdateTunnel(ctx, nl); err != nil {
		t.Fatalf("UpdateTunnel: %v", err)
	}

	if _, err := s.EnsureBuiltinCountryPools(ctx, catalogURL, CountryPools(), TunnelVLESS); err != nil {
		t.Fatalf("повторный EnsureBuiltinCountryPools: %v", err)
	}
	list, err = s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(list) != len(CountryPools()) {
		t.Fatalf("после переименования туннелей %d, ожидалось %d", len(list), len(CountryPools()))
	}
	got := countryOf(list)["NL"]
	if got.ID != nl.ID || got.Name != "Мой любимый пул" {
		t.Errorf("пул NL подменён или переименован обратно: %+v", got)
	}
	// Country и Builtin переживают правку через UpdateTunnel.
	if got.Country != "NL" || !got.Builtin {
		t.Errorf("правка сбила страну или признак встроенного: %+v", got)
	}
}

// Старый единственный пул без страны удаляется, а ссылавшиеся на него правила
// отвязываются: первое звено — с выключением правила, второе звено цепи — с
// сохранением. Всё видно в результате, чтобы демон написал об этом в лог.
func TestEnsureBuiltinCountryPoolsRemovesLegacy(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	legacy := builtinPool(t, s, "Бесплатные ключи")
	// Правило, у которого пул — первое (основное) звено.
	primary, err := s.CreateRule(ctx, sampleRule("В пул напрямую", legacy.ID))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	// Правило, у которого пул — второе звено цепи, а первое — обычный туннель.
	head, err := s.CreateTunnel(ctx, sampleTunnel("голова цепи"))
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	chained := sampleRule("Через цепь", head.ID)
	chained.ViaTunnelID = legacy.ID
	chained, err = s.CreateRule(ctx, chained)
	if err != nil {
		t.Fatalf("CreateRule (цепь): %v", err)
	}

	res, err := s.EnsureBuiltinCountryPools(ctx, catalogURL, CountryPools(), TunnelVLESS)
	if err != nil {
		t.Fatalf("EnsureBuiltinCountryPools: %v", err)
	}
	if len(res.RemovedLegacy) != 1 || res.RemovedLegacy[0] != legacy.Name {
		t.Fatalf("старый пул не удалён из результата: %+v", res.RemovedLegacy)
	}
	if len(res.DetachedRules) != 2 {
		t.Fatalf("отвязано правил %d, ожидалось 2: %+v", len(res.DetachedRules), res.DetachedRules)
	}

	// Старого пула в БД нет, страновые есть.
	if _, err := s.Tunnel(ctx, legacy.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("старый пул уцелел: %v", err)
	}
	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(countryOf(list)) != len(CountryPools()) {
		t.Errorf("страновых пулов %d, ожидалось %d", len(countryOf(list)), len(CountryPools()))
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

	// Повторный старт удалять уже нечего.
	again, err := s.EnsureBuiltinCountryPools(ctx, catalogURL, CountryPools(), TunnelVLESS)
	if err != nil {
		t.Fatalf("повторный EnsureBuiltinCountryPools: %v", err)
	}
	if len(again.RemovedLegacy) != 0 || len(again.DetachedRules) != 0 || len(again.Created) != 0 {
		t.Errorf("повторный вызов снова что-то менял: %+v", again)
	}
}

// Пустой адрес каталога — отказ: пул без источника бесполезен.
func TestEnsureBuiltinCountryPoolsRejectsEmptyCatalog(t *testing.T) {
	s := open(t)
	if _, err := s.EnsureBuiltinCountryPools(context.Background(), "", CountryPools(), TunnelVLESS); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ожидалась ErrInvalid, получено: %v", err)
	}
}

// Встроенную запись не удаляют, а выключают: на следующем старте демон завёл бы её
// снова, и «удалил» оказалось бы неправдой.
func TestDeleteBuiltinPoolIsRejected(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.EnsureBuiltinCountryPools(ctx, catalogURL, CountryPools(), TunnelVLESS); err != nil {
		t.Fatalf("EnsureBuiltinCountryPools: %v", err)
	}
	list, err := s.Tunnels(ctx)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	pool := countryOf(list)["NL"]

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
