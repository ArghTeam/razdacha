package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Границы MTU клиентов (ADR 0004). Нижняя — само значение решения: 1280 это
// минимальный MTU IPv6, и на него же настроен MSS-клампинг; всё, что ниже,
// режет полезную нагрузку без причины и расходится с инвариантом конституции.
// Верхняя — то самое «1420 для максимальной скорости в надёжной сети», которое
// ADR 0004 оставляет в UI для тех, кто понимает, что делает; выше начинается
// дефолт системы, а его решение запрещает прямо.
//
// Прежние 576–1500 не были границами решения вовсе: 576 — минимальный IPv4 MTU
// из RFC 791, к нашей двойной инкапсуляции отношения не имеющий.
const (
	MinClientMTU = 1280
	MaxClientMTU = 1420
)

// MinPoolUpdateInterval — нижняя граница интервала обхода каталога пула.
//
// Полчаса не вкусовая цифра: выселение сервера из пула требует
// `poolMissesBeforeDrop` пропусков подряд (три, слой lists), и floor в 30 минут
// держит выселение не быстрее чем за полтора часа — churn состава и перезапуски
// sing-box не растут, сколько бы пользователь ни ужимал интервал.
const MinPoolUpdateInterval = 30 * time.Minute

// Settings — единственная запись настроек. В БД лежит как key/value, чтобы добавление
// поля не требовало миграции; отсутствующие ключи берутся из [DefaultSettings].
type Settings struct {
	WGListenPort       int           `json:"wg_listen_port"`
	WGPool             string        `json:"wg_pool"`
	WGServerAddress    string        `json:"wg_server_address"`
	EndpointHost       string        `json:"endpoint_host"`
	ClientMTU          int           `json:"client_mtu"`
	DNSUpstream        string        `json:"dns_upstream"`
	DNSType            string        `json:"dns_type"`
	WANInterface       string        `json:"wan_interface"`
	ListUpdateInterval time.Duration `json:"list_update_interval"`
	// PoolUpdateInterval — как часто расписание обходит каталог ключей пула.
	// Дефолт 1 ч, нижняя граница [MinPoolUpdateInterval].
	PoolUpdateInterval time.Duration `json:"pool_update_interval"`
	// PoolCountryBlocklist — ISO-коды стран, ноды которых в пул не берутся
	// (ADR 0020). Дефолт RU, BY; пустой список означает «не отбраковывать по
	// стране» и является законным выбором пользователя, а не отсутствием ключа.
	PoolCountryBlocklist []string `json:"pool_country_blocklist"`
	// TunnelCheckInterval — как часто расписание опрашивает состояние туннелей.
	TunnelCheckInterval time.Duration `json:"tunnel_check_interval"`
	LogLevel            string        `json:"log_level"`
}

// DefaultSettings — дефолты из docs/02-data-model.md. MTU 1280 — не «по умолчанию
// системы», а требование ADR 0004; пустые endpoint_host и wan_interface означают
// автодетект на слое netstack.
func DefaultSettings() Settings {
	return Settings{
		WGListenPort:       51820,
		WGPool:             "10.8.0.0/24",
		WGServerAddress:    "10.8.0.1",
		EndpointHost:       "",
		ClientMTU:          1280,
		DNSUpstream:        "1.1.1.1",
		DNSType:            "udp",
		WANInterface:       "",
		ListUpdateInterval: 24 * time.Hour,
		// Один час — тот же дефолт, что и `lists.DefaultPoolInterval`; держатся
		// равными руками, потому что store не может импортировать lists (это он
		// импортирует store). Разъедутся — расписание возьмёт свой fallback.
		PoolUpdateInterval: 1 * time.Hour,
		// Дефолт живёт в коде, а не в схеме: настройки лежат key/value, и
		// отсутствующий ключ читается отсюда. На уже установленном сервере
		// чёрный список применяется без миграции и без правки БД руками (ADR 0020).
		PoolCountryBlocklist: DefaultPoolCountryBlocklist(),
		// Две минуты — компромисс: чаще означает лишние пробы через каждый
		// обычный туннель, реже — что падение замечается слишком поздно, а
		// подтверждение перехода тремя проверками растягивается на полчаса.
		TunnelCheckInterval: 2 * time.Minute,
		LogLevel:            "warn",
	}
}

// Ключи в таблице settings. Переименование ключа — миграция, а не правка константы.
const (
	keyWGListenPort        = "wg_listen_port"
	keyWGPool              = "wg_pool"
	keyWGServerAddress     = "wg_server_address"
	keyEndpointHost        = "endpoint_host"
	keyClientMTU           = "client_mtu"
	keyDNSUpstream         = "dns_upstream"
	keyDNSType             = "dns_type"
	keyWANInterface        = "wan_interface"
	keyListUpdateInterval  = "list_update_interval"
	keyPoolUpdateInterval  = "pool_update_interval"
	keyPoolCountryBlock    = "pool_country_blocklist"
	keyTunnelCheckInterval = "tunnel_check_interval"
	keyLogLevel            = "log_level"
)

// Settings читает настройки, подставляя дефолты вместо отсутствующих ключей.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	return settings(ctx, s.db)
}

func settings(ctx context.Context, q querier) (Settings, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, fmt.Errorf("чтение настроек: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := DefaultSettings()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Settings{}, fmt.Errorf("чтение настроек: %w", err)
		}
		if err := out.set(key, value); err != nil {
			return Settings{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return Settings{}, fmt.Errorf("чтение настроек: %w", err)
	}
	return out, nil
}

// SaveSettings перезаписывает все настройки целиком.
func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	if err := v.validate(); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		for key, value := range v.values() {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO settings (key, value) VALUES (?, ?)
				 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
				key, value); err != nil {
				return fmt.Errorf("запись настройки %s: %w", key, err)
			}
		}
		return nil
	})
}

// values раскладывает настройки по ключам таблицы.
func (v Settings) values() map[string]string {
	return map[string]string{
		keyWGListenPort:       strconv.Itoa(v.WGListenPort),
		keyWGPool:             v.WGPool,
		keyWGServerAddress:    v.WGServerAddress,
		keyEndpointHost:       v.EndpointHost,
		keyClientMTU:          strconv.Itoa(v.ClientMTU),
		keyDNSUpstream:        v.DNSUpstream,
		keyDNSType:            v.DNSType,
		keyWANInterface:       v.WANInterface,
		keyListUpdateInterval: strconv.FormatInt(int64(v.ListUpdateInterval/time.Second), 10),
		keyPoolUpdateInterval: strconv.FormatInt(int64(v.PoolUpdateInterval/time.Second), 10),
		// Через запятую, без пробелов. Пустая строка — законное значение: она
		// означает «по стране не отбраковывать», в отличие от отсутствия ключа,
		// который читается дефолтом (ADR 0020).
		keyPoolCountryBlock: strings.Join(v.PoolCountryBlocklist, ","),
		keyTunnelCheckInterval: strconv.FormatInt(
			int64(v.TunnelCheckInterval/time.Second), 10),
		keyLogLevel: v.LogLevel,
	}
}

// set применяет одно значение из БД. Неизвестные ключи игнорируются: откат на
// предыдущую версию демона не должен ронять чтение настроек.
func (v *Settings) set(key, value string) error {
	number := func(dst *int) error {
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%w: настройка %s = %q не число", ErrInvalid, key, value)
		}
		*dst = n
		return nil
	}

	switch key {
	case keyWGListenPort:
		return number(&v.WGListenPort)
	case keyClientMTU:
		return number(&v.ClientMTU)
	case keyListUpdateInterval:
		var seconds int
		if err := number(&seconds); err != nil {
			return err
		}
		v.ListUpdateInterval = time.Duration(seconds) * time.Second
	case keyPoolUpdateInterval:
		var seconds int
		if err := number(&seconds); err != nil {
			return err
		}
		v.PoolUpdateInterval = time.Duration(seconds) * time.Second
	case keyTunnelCheckInterval:
		var seconds int
		if err := number(&seconds); err != nil {
			return err
		}
		v.TunnelCheckInterval = time.Duration(seconds) * time.Second
	case keyPoolCountryBlock:
		v.PoolCountryBlocklist = parseCountryList(value)
	case keyWGPool:
		v.WGPool = value
	case keyWGServerAddress:
		v.WGServerAddress = value
	case keyEndpointHost:
		v.EndpointHost = value
	case keyDNSUpstream:
		v.DNSUpstream = value
	case keyDNSType:
		v.DNSType = value
	case keyWANInterface:
		v.WANInterface = value
	case keyLogLevel:
		v.LogLevel = value
	}
	return nil
}

// parseCountryList разбирает чёрный список стран из строки «RU,BY».
//
// Пустой список — не то же самое, что отсутствие ключа: отсутствие берётся из
// [DefaultSettings], а пустая строка означает «по стране не отбраковывать». Регистр
// и пробелы нормализуются здесь, чтобы сравнение в [PoolFilter] не занималось этим на
// каждой ноде.
func parseCountryList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if code := strings.ToUpper(strings.TrimSpace(part)); code != "" {
			out = append(out, code)
		}
	}
	return out
}

// isCountryCode — две латинские буквы в верхнем регистре.
func isCountryCode(code string) bool {
	if len(code) != 2 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < 'A' || code[i] > 'Z' {
			return false
		}
	}
	return true
}

// validate проверяет настройки до записи.
func (v Settings) validate() error {
	if v.WGListenPort < 1 || v.WGListenPort > 65535 {
		return fmt.Errorf("%w: порт WireGuard %d вне диапазона 1–65535", ErrInvalid, v.WGListenPort)
	}
	if v.ClientMTU < MinClientMTU || v.ClientMTU > MaxClientMTU {
		return fmt.Errorf("%w: MTU %d вне диапазона %d–%d",
			ErrInvalid, v.ClientMTU, MinClientMTU, MaxClientMTU)
	}
	if v.WGPool == "" || v.WGServerAddress == "" {
		return fmt.Errorf("%w: пул адресов и адрес сервера обязательны", ErrInvalid)
	}
	// Адреса разбираются здесь, а не только на слое netstack: непустая строка с
	// опечаткой доезжала до `WGConfigFromSettings` и роняла **старт демона**, то
	// есть шлюз не поднимался вовсе, а панель на записи молчала (аудит от
	// 2026-07, пункт 11). Ручка обязана отказать раньше, чем это попадёт в БД.
	pool, err := netip.ParsePrefix(v.WGPool)
	if err != nil {
		return fmt.Errorf("%w: пул адресов %q — не подсеть вида 10.8.0.0/24", ErrInvalid, v.WGPool)
	}
	if !pool.Addr().Is4() {
		return fmt.Errorf("%w: пул адресов %q не IPv4", ErrInvalid, v.WGPool)
	}
	addr, err := netip.ParseAddr(v.WGServerAddress)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("%w: адрес сервера %q не IPv4-адрес", ErrInvalid, v.WGServerAddress)
	}
	if !pool.Contains(addr) {
		return fmt.Errorf("%w: адрес сервера %s вне пула %s", ErrInvalid, addr, pool)
	}
	if v.DNSUpstream == "" {
		return fmt.Errorf("%w: апстрим DNS обязателен", ErrInvalid)
	}
	switch v.DNSType {
	case "udp", "dot", "doh":
	default:
		return fmt.Errorf("%w: неизвестный тип DNS %q", ErrInvalid, v.DNSType)
	}
	switch v.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("%w: неизвестный уровень логов %q", ErrInvalid, v.LogLevel)
	}
	if v.ListUpdateInterval <= 0 {
		return fmt.Errorf("%w: интервал обновления списков должен быть положительным", ErrInvalid)
	}
	// Нижняя граница держит churn состава пула в узде — см. MinPoolUpdateInterval.
	if v.PoolUpdateInterval < MinPoolUpdateInterval {
		return fmt.Errorf("%w: интервал обновления пула меньше %s", ErrInvalid, MinPoolUpdateInterval)
	}
	// Код страны — две латинские буквы (ISO 3166-1 alpha-2). Отказ на записи, а не
	// тихая нормализация: «Россия» в чёрном списке выглядела бы работающей, а
	// фильтр сверяется с флагом и кодом, и такая запись не совпала бы ни с чем.
	for _, code := range v.PoolCountryBlocklist {
		if !isCountryCode(code) {
			return fmt.Errorf("%w: %q — не код страны из двух латинских букв (например RU)",
				ErrInvalid, code)
		}
	}
	// Нижняя граница не вкусовая: каждый прогон пробивает обычные туннели
	// настоящим запросом, и секундный интервал превратил бы проверку в нагрузку.
	if v.TunnelCheckInterval < 30*time.Second {
		return fmt.Errorf("%w: интервал проверки туннелей меньше 30 секунд", ErrInvalid)
	}
	return nil
}
