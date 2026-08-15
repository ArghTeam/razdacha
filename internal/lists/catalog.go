package lists

// CommunityService — запись каталога готовых списков. Панель показывает их
// галочками в форме правила, а ключ кладёт в `Rule.community_lists`
// (docs/02-data-model.md). Поля совпадают с телом `GET /api/lists/community`.
type CommunityService struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	HasDomains bool   `json:"has_domains"`
	HasSubnets bool   `json:"has_subnets"`
	// InPreset — сервис входит в стартовый пресет ([presetKeys]).
	InPreset bool `json:"in_preset"`
}

// communityCatalog — сервисы allow-domains в том же составе и порядке, что у
// Podkop (podkop/files/usr/lib/constants.sh:66). Названия на русском: они
// показываются пользователю как есть.
//
// Порядок задан списком, а не картой: в форме правила он определяет порядок
// галочек, и сортировка по ключу перемешала бы тематические списки с сервисами.
var communityCatalog = []struct {
	key   string
	title string
}{
	{"russia_inside", "Россия — внутренние сервисы"},
	{"russia_outside", "Россия — зарубежные сервисы"},
	{"ukraine_inside", "Украина — внутренние сервисы"},
	{"geoblock", "Блокирующие Россию"},
	{"block", "Заблокированные в России"},
	{"porn", "Взрослый контент"},
	{"news", "Новости"},
	{"anime", "Аниме"},
	{"youtube", "YouTube"},
	{"hdrezka", "HDRezka"},
	{"tiktok", "TikTok"},
	{"google_ai", "Google AI"},
	{"google_play", "Google Play"},
	{"hodca", "H.O.D.C.A"},
	{"discord", "Discord"},
	{"meta", "Meta (Facebook, Instagram)"},
	{"twitter", "Twitter (X)"},
	{"cloudflare", "Cloudflare"},
	{"cloudfront", "CloudFront (ASN)"},
	{"digitalocean", "DigitalOcean (ASN)"},
	{"hetzner", "Hetzner (ASN)"},
	{"ovh", "OVH (ASN)"},
	{"telegram", "Telegram"},
	{"roblox", "Roblox"},
}

/* --- стартовый пресет -----------------------------------------------------
   Набор ключей каталога, который панель предлагает кнопкой на пустом экране
   правил (ADR 0014). Ни туннелей, ни адресов списков здесь нет и быть не
   может: пресет обязан пережить сервер с другим набором того и другого.

   Направление у пресета одно — «в туннель». Строк «напрямую» нет: `route.final`
   и так прямой выход, и правило `direct` ничего не меняет, пока выше нет
   широкого правила в туннель.

   Состав держится на двух ограничениях Podkop, оба проверены грепом:

     - региональные списки взаимно исключают друг друга
       (`REGIONAL_OPTIONS`, luci-app-podkop/htdocs/luci-static/resources/view/podkop/main.js:875);
     - `russia_inside` — надмножество почти всего каталога: при его выборе
       Podkop снимает все ключи, кроме перечисленных в `ALLOWED_WITH_RUSSIA_INSIDE`
       (там же, строка 880; снятие — section.js:344), потому что остальные уже
       внутри него.

   Отсюда и раскладка: базой берётся `russia_inside` — ровно то, что Podkop
   кладёт в дефолтный конфиг (podkop/files/etc/config/podkop:29), — а сверху
   только те сервисы, которых в нём нет.

   Чего в пресете нет и почему:

     - `russia_outside`, `ukraine_inside` — второй региональный список рядом с
       первым Podkop снимает; выбор региона за пользователем, пресет один;
     - `telegram`, `google_play` — в России работают, гнать их в туннель по
       умолчанию значит утяжелять его без повода;
     - `cloudflare`, `cloudfront`, `hetzner`, `ovh`, `digitalocean` — списки
       подсетей целых ASN: за ними лежит половина интернета, включая российские
       сайты, и селективный шлюз превратился бы в сплошной;
     - `porn`, `news`, `anime`, `roblox`, `hodca` — вкусовые наборы, их
       добавляют галочкой в той же форме;
     - `block`, `geoblock` — уже внутри `russia_inside` (их нет в
       `ALLOWED_WITH_RUSSIA_INSIDE`), отдельной строкой это был бы дубль.

   Список — срез, а не карта: порядок задаёт порядок перечисления в панели. */
var presetKeys = []string{
	"russia_inside",
	"meta",
	"twitter",
	"discord",
	"google_ai",
}

// presetSet — те же ключи для проверки принадлежности.
var presetSet = func() map[string]bool {
	m := make(map[string]bool, len(presetKeys))
	for _, k := range presetKeys {
		m[k] = true
	}
	return m
}()

// Preset — ключи стартового пресета в порядке перечисления. Копия: состав
// каталога наружу не редактируется.
func Preset() []string {
	out := make([]string, len(presetKeys))
	copy(out, presetKeys)
	return out
}

// Catalog — доступные сервисы. Домены есть у каждого: `.srs` собирается на
// каждый ключ (podkop/files/usr/bin/podkop:1002); подсети — только у тех, что
// перечислены в [communitySubnets], и признак берётся оттуда, чтобы каталог и
// планировщик не разъезжались.
//
// Каталог статический: он описывает состав чужого репозитория, а не состояние
// демона, поэтому ни сети, ни БД для ответа не нужно.
func Catalog() []CommunityService {
	out := make([]CommunityService, 0, len(communityCatalog))
	for _, s := range communityCatalog {
		out = append(out, CommunityService{
			Key:        s.key,
			Title:      s.title,
			HasDomains: true,
			HasSubnets: communitySubnets[s.key],
			InPreset:   presetSet[s.key],
		})
	}
	return out
}
