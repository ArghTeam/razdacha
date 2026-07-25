// Моки для прототипа. Формы объектов повторяют docs/05-api.md и internal/store/model.go:
// то, что лежит в БД, — как в модели; производные поля (online, last_handshake,
// rx_bytes/tx_bytes, endpoint у пира; status, latency_ms, last_check у туннеля)
// в реальности приходят от демона по WS, здесь просто дописаны в те же объекты.

// Смещения хендшейков заданы в секундах от загрузки страницы, чтобы «онлайн» считался
// тем же правилом, что и в демоне: хендшейк менее 3 минут назад.
const now = () => Date.now();
const ago = (sec) => new Date(now() - sec * 1000).toISOString();

const MOCK = {
  // GET /api/settings
  settings: {
    wg_listen_port: 51820,
    wg_pool: '10.8.0.0/24',
    wg_server_address: '10.8.0.1',
    endpoint_host: 'vpn.example.com',
    client_mtu: 1280,
    dns_upstream: '1.1.1.1',
    dns_type: 'udp',
    wan_interface: 'eth0',
    list_update_interval: 86400,
    log_level: 'warn',
  },

  // Публичный ключ сервера — в API отдельного поля нет, он приходит внутри .conf пира.
  server_public_key: 'kL9pQ2xVn3tYb7mR4sE1wZ8cJ6hA0dF5gU2iO7yT3nQ=',

  // GET /api/peers
  peers: [
    {
      id: 'p-01', name: 'iPhone Ромы', address: '10.8.0.5', enabled: true,
      public_key: 'Xy7Qa2LmN4pR8sT1uV3wY5zB6cD9eF0gH2iJ4kL6mN8=',
      private_key: 'aB3cD5eF7gH9iJ1kL3mN5oP7qR9sT1uV3wX5yZ7aB9c=',
      preshared_key: 'zQ8wE6rT4yU2iO0pA9sD7fG5hJ3kL1zX9cV7bN5mQ3w=',
      created_at: ago(86400 * 41),
      last_handshake: ago(12), rx_bytes: 184320000, tx_bytes: 12800000,
      endpoint: '203.0.113.44:41233',
    },
    {
      id: 'p-02', name: 'MacBook', address: '10.8.0.6', enabled: true,
      public_key: 'Mn4Bv6Cx8Zl0Ks2Jd4Hf6Ga8Sq0We2Rt4Yu6Io8Pa0Sd=',
      private_key: 'qW2eR4tY6uI8oP0aS2dF4gH6jK8lZ0xC2vB4nM6qW8e=',
      preshared_key: 'pO9iU7yT5rE3wQ1aZ9xS7dC5fV3gB1hN9jM7kL5pO3i=',
      created_at: ago(86400 * 41),
      last_handshake: ago(47), rx_bytes: 2254857830, tx_bytes: 356515840,
      endpoint: '198.51.100.19:52104',
    },
    {
      id: 'p-03', name: 'Телевизор в гостиной', address: '10.8.0.7', enabled: true,
      public_key: 'Tv1Ee3Rr5Tt7Yy9Uu1Ii3Oo5Pp7Aa9Ss1Dd3Ff5Gg7Hh=',
      private_key: 'zX1cV3bN5mQ7wE9rT1yU3iO5pA7sD9fG1hJ3kL5zX7c=',
      preshared_key: 'lK4jH6gF8dS0aP2oI4uY6tR8eW0qZ2xC4vB6nM8lK0j=',
      created_at: ago(86400 * 30),
      last_handshake: ago(86400 * 3 + 3600), rx_bytes: 41943040, tx_bytes: 2097152,
      endpoint: '203.0.113.44:39811',
    },
    {
      id: 'p-04', name: 'Ноутбук Кати', address: '10.8.0.8', enabled: true,
      public_key: 'Kk2Ll4Mm6Nn8Oo0Pp2Qq4Rr6Ss8Tt0Uu2Vv4Ww6Xx8Yy=',
      private_key: 'mN8bV6cX4zL2kJ0hG8fD6sA4pO2iU0yT8rE6wQ4mN2b=',
      preshared_key: 'bN7mQ5wE3rT1yU9iO7pA5sD3fG1hJ9kL7zX5cV3bN1m=',
      created_at: ago(86400 * 12),
      last_handshake: ago(4 * 3600 + 900), rx_bytes: 738197504, tx_bytes: 91750400,
      endpoint: '203.0.113.44:44902',
    },
    {
      id: 'p-05', name: 'Планшет (отключён)', address: '10.8.0.9', enabled: false,
      public_key: 'Pp3Ll5Aa7Nn9Ss1Hh3Ee5Tt7Zz9Qq1Ww3Ee5Rr7Tt9Yy=',
      private_key: 'cV5bN3mQ1wE9rT7yU5iO3pA1sD9fG7hJ5kL3zX1cV9b=',
      preshared_key: 'aS1dF3gH5jK7lZ9xC1vB3nM5qW7eR9tY1uI3oP5aS7d=',
      created_at: ago(86400 * 5),
      last_handshake: null, rx_bytes: 0, tx_bytes: 0, endpoint: null,
    },
  ],

  // GET /api/tunnels
  tunnels: [
    {
      id: 't-nl', name: 'Нидерланды', type: 'vless', source: 'url', enabled: true,
      raw: 'vless://3f8c1a4e-9b2d-4c77-8e51-1a2b3c4d5e6f@nl.example.com:443?security=reality&sni=www.microsoft.com&fp=chrome&pbk=8Xk2q0Zt9YbM3nR7sE1wZ8cJ6hA0dF5gU2iO7yT3nQ&type=tcp&flow=xtls-rprx-vision#NL',
      parsed: { type: 'vless', server: 'nl.example.com', server_port: 443, security: 'reality', transport: 'tcp' },
      created_at: ago(86400 * 40),
      status: 'up', latency_ms: 42, last_check: ago(38),
    },
    {
      id: 't-home', name: 'Домашний wg', type: 'wireguard', source: 'wg_conf', enabled: true,
      raw: '[Interface]\nPrivateKey = 4Hf6Ga8Sq0We2Rt4Yu6Io8Pa0Sd2Fg4Hj6Kl8Zx0Cv=\nAddress = 10.14.0.2/32\n\n[Peer]\nPublicKey = 9Uu1Ii3Oo5Pp7Aa9Ss1Dd3Ff5Gg7Hh9Jj1Kk3Ll5Mm=\nEndpoint = 198.51.100.7:51820\nAllowedIPs = 0.0.0.0/0',
      parsed: { type: 'wireguard', server: '198.51.100.7', server_port: 51820 },
      created_at: ago(86400 * 22),
      status: 'up', latency_ms: 18, last_check: ago(38),
    },
    {
      id: 't-bak', name: 'Резервный', type: 'hysteria2', source: 'url', enabled: true,
      raw: 'hysteria2://s3cr3tp4ss@hy.example.net:8443?sni=hy.example.net&insecure=0#backup',
      parsed: { type: 'hysteria2', server: 'hy.example.net', server_port: 8443 },
      created_at: ago(86400 * 9),
      status: 'down', latency_ms: null, last_check: ago(38),
    },
  ],

  // GET /api/rules — уже по возрастанию priority.
  // Правила 2 и 4 намеренно делят список «google»: правило 4 никогда не сработает,
  // пока правило 2 выше. Это и есть то, ради чего приоритет виден в интерфейсе.
  rules: [
    {
      id: 'r-1', name: 'Банки и госуслуги', action: 'direct', tunnel_id: null,
      priority: 0, enabled: true,
      community_lists: ['gosuslugi', 'sberbank', 'tinkoff'],
      domains: ['nalog.gov.ru', 'mos.ru'], subnets: [], remote_lists: [],
      peer_scope: 'all', peer_ids: [], resolve_real_ip: true,
    },
    {
      id: 'r-2', name: 'YouTube и Google', action: 'tunnel', tunnel_id: 't-nl',
      priority: 1, enabled: true,
      community_lists: ['youtube', 'google'],
      domains: [], subnets: [], remote_lists: [],
      peer_scope: 'all', peer_ids: [], resolve_real_ip: false,
    },
    {
      id: 'r-3', name: 'Соцсети и мессенджеры', action: 'tunnel', tunnel_id: 't-nl',
      priority: 2, enabled: true,
      community_lists: ['meta', 'twitter', 'tiktok', 'discord'],
      domains: ['pikabu.ru'], subnets: [], remote_lists: [],
      peer_scope: 'all', peer_ids: [], resolve_real_ip: false,
    },
    {
      id: 'r-4', name: 'Рабочее — через дом', action: 'tunnel', tunnel_id: 't-home',
      priority: 3, enabled: true,
      community_lists: ['google', 'github'],
      domains: ['jira.corp.example'], subnets: ['203.0.113.0/24', '198.51.100.0/24'],
      remote_lists: [], peer_scope: 'selected', peer_ids: ['p-02'], resolve_real_ip: false,
    },
    {
      id: 'r-5', name: 'Реклама и трекеры', action: 'block', tunnel_id: null,
      priority: 4, enabled: false,
      community_lists: ['ads', 'trackers'],
      domains: [], subnets: [], remote_lists: ['https://example.org/blocklist.srs'],
      peer_scope: 'all', peer_ids: [], resolve_real_ip: false,
    },
  ],

  // GET /api/lists/community
  community_lists: [
    { key: 'youtube', title: 'YouTube', has_domains: true, has_subnets: true },
    { key: 'google', title: 'Google', has_domains: true, has_subnets: true },
    { key: 'meta', title: 'Meta', has_domains: true, has_subnets: true },
    { key: 'twitter', title: 'Twitter / X', has_domains: true, has_subnets: true },
    { key: 'tiktok', title: 'TikTok', has_domains: true, has_subnets: false },
    { key: 'telegram', title: 'Telegram', has_domains: true, has_subnets: true },
    { key: 'discord', title: 'Discord', has_domains: true, has_subnets: true },
    { key: 'cloudflare', title: 'Cloudflare', has_domains: true, has_subnets: true },
    { key: 'github', title: 'GitHub', has_domains: true, has_subnets: true },
    { key: 'openai', title: 'OpenAI', has_domains: true, has_subnets: false },
    { key: 'anthropic', title: 'Anthropic', has_domains: true, has_subnets: false },
    { key: 'netflix', title: 'Netflix', has_domains: true, has_subnets: true },
    { key: 'spotify', title: 'Spotify', has_domains: true, has_subnets: true },
    { key: 'twitch', title: 'Twitch', has_domains: true, has_subnets: true },
    { key: 'steam', title: 'Steam', has_domains: true, has_subnets: true },
    { key: 'epicgames', title: 'Epic Games', has_domains: true, has_subnets: false },
    { key: 'amazon', title: 'Amazon', has_domains: true, has_subnets: true },
    { key: 'apple', title: 'Apple', has_domains: true, has_subnets: true },
    { key: 'microsoft', title: 'Microsoft', has_domains: true, has_subnets: true },
    { key: 'gosuslugi', title: 'Госуслуги', has_domains: true, has_subnets: false },
    { key: 'sberbank', title: 'Сбербанк', has_domains: true, has_subnets: true },
    { key: 'tinkoff', title: 'Т-Банк', has_domains: true, has_subnets: true },
    { key: 'ads', title: 'Реклама', has_domains: true, has_subnets: false },
    { key: 'trackers', title: 'Трекеры', has_domains: true, has_subnets: false },
  ],

  // GET /api/diag
  diag: {
    overall: 'warn',
    checks: [
      { id: 'wg', title: 'WireGuard', status: 'ok', detail: 'wg0 поднят, 5 пиров, 2 онлайн' },
      { id: 'singbox', title: 'sing-box', status: 'ok', detail: '1.12.4, запущен 4 ч назад' },
      { id: 'nft', title: 'Правила nftables', status: 'ok', detail: 'таблица razdacha, 1284 подсети в сете' },
      { id: 'tunnels', title: 'Туннели', status: 'warn', detail: '«Резервный» не отвечает' },
      { id: 'lists', title: 'Списки', status: 'ok', detail: 'обновлены 3 ч назад' },
      { id: 'forward', title: 'IP forwarding', status: 'ok', detail: 'net.ipv4.ip_forward = 1' },
      { id: 'mtu', title: 'Path MTU', status: 'ok', detail: '1500 до всех туннелей' },
    ],
  },

  // GET /api/logs?source=razdachad
  logs: {
    razdachad: [
      '2026-07-25T09:12:04Z INFO  запуск razdachad 0.1.0-dev, go1.23.4',
      '2026-07-25T09:12:04Z INFO  wg0 поднят: 10.8.0.1/24, порт 51820, MTU 1280',
      '2026-07-25T09:12:05Z INFO  nftables: таблица razdacha создана, 3 цепочки',
      '2026-07-25T09:12:05Z INFO  конфиг sing-box сгенерирован, sing-box check пройден',
      '2026-07-25T09:12:06Z INFO  sing-box запущен, pid 4412',
      '2026-07-25T11:44:31Z WARN  туннель «Резервный»: таймаут проверки (3s)',
      '2026-07-25T12:03:11Z INFO  хендшейк 10.8.0.5 (iPhone Ромы)',
      '2026-07-25T13:07:52Z WARN  туннель «Резервный»: таймаут проверки (3s)',
    ],
    'sing-box': [
      'INFO[0000] router: loaded rule-set youtube (2143 items)',
      'INFO[0000] router: loaded rule-set google (5011 items)',
      'INFO[0000] dns: fakeip range 198.18.0.0/15, strategy ipv4_only',
      'INFO[0001] outbound/vless[tun-t-nl]: connected to nl.example.com:443',
      'ERROR[0132] outbound/hysteria2[tun-t-bak]: dial failed: i/o timeout',
    ],
  },

  // GET /api/diag/singbox-config — сокращённый фрагмент, для просмотра «как есть».
  singbox_config: `{
  "log": { "level": "warn" },
  "dns": {
    "servers": [
      { "tag": "upstream", "address": "1.1.1.1", "strategy": "ipv4_only" },
      { "tag": "fakeip", "address": "fakeip" }
    ],
    "fakeip": { "enabled": true, "inet4_range": "198.18.0.0/15" },
    "strategy": "ipv4_only"
  },
  "inbounds": [
    { "type": "tun", "tag": "wg-in", "interface_name": "wg0", "mtu": 1280 }
  ],
  "endpoints": [
    { "type": "wireguard", "tag": "tun-t-home", "system": false,
      "address": ["10.14.0.2/32"], "mtu": 1280,
      "peers": [{ "address": "198.51.100.7", "port": 51820 }] }
  ],
  "outbounds": [
    { "type": "vless", "tag": "tun-t-nl", "server": "nl.example.com", "server_port": 443 },
    { "type": "hysteria2", "tag": "tun-t-bak", "server": "hy.example.net", "server_port": 8443 },
    { "type": "direct", "tag": "direct-out" }
  ],
  "route": {
    "rules": [
      { "rule_set": ["rs-r-1-lists"], "outbound": "direct-out" },
      { "rule_set": ["rs-r-2-lists"], "outbound": "tun-t-nl" },
      { "rule_set": ["rs-r-3-lists", "rs-r-3-inline"], "outbound": "tun-t-nl" },
      { "rule_set": ["rs-r-4-lists"], "source_ip_cidr": ["10.8.0.6/32"], "outbound": "tun-t-home" }
    ]
  }
}`,
};
