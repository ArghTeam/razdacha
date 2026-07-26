package lists

// CommunityService — запись каталога готовых списков. Панель показывает их
// галочками в форме правила, а ключ кладёт в `Rule.community_lists`
// (docs/02-data-model.md). Поля совпадают с телом `GET /api/lists/community`.
type CommunityService struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	HasDomains bool   `json:"has_domains"`
	HasSubnets bool   `json:"has_subnets"`
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
		})
	}
	return out
}
