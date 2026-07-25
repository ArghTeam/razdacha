package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/ArghTeam/razdacha/internal/store"
)

// ErrNoAddress — в пуле не осталось свободных адресов.
var ErrNoAddress = errors.New("в пуле не осталось свободных адресов")

// peerResponse — пир, как его видит UI.
//
// Приватный и pre-shared ключи в списке не отдаются: наружу они уходят ровно один
// раз и целиком — в клиентском конфиге `/api/peers/{id}/config`.
//
// Производные поля — указатели, и это не стиль: источник у них один, слой netstack,
// которого ещё нет. `null` означает «данных нет», ноль означал бы «замерили и там
// ноль» — для «онлайн» и «последний хендшейк» это противоположные состояния.
type peerResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PublicKey string    `json:"public_key"`
	Address   string    `json:"address"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`

	Online        *bool      `json:"online"`
	LastHandshake *time.Time `json:"last_handshake"`
	RxBytes       *int64     `json:"rx_bytes"`
	TxBytes       *int64     `json:"tx_bytes"`
	Endpoint      *string    `json:"endpoint"`
}

// newPeerResponse собирает ответ по записи БД. Производные поля остаются nil,
// пока их некому заполнить.
func newPeerResponse(p store.Peer) peerResponse {
	return peerResponse{
		ID:        p.ID,
		Name:      p.Name,
		PublicKey: p.PublicKey,
		Address:   p.Address,
		Enabled:   p.Enabled,
		CreatedAt: p.CreatedAt.UTC(),
	}
}

// handleListPeers — `GET /api/peers`.
func (s *Server) handleListPeers(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Peers(r.Context())
	if err != nil {
		s.storeError(w, err, "Пир не найден")
		return
	}
	out := make([]peerResponse, 0, len(list))
	for _, p := range list {
		out = append(out, newPeerResponse(p))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// createPeerRequest — тело `POST /api/peers`. Ключи и адрес не принимаются:
// их выдаёт демон, иначе два пира легко получают один адрес.
type createPeerRequest struct {
	Name string `json:"name"`
}

// handleCreatePeer — `POST /api/peers`.
func (s *Server) handleCreatePeer(w http.ResponseWriter, r *http.Request) {
	var req createPeerRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Не указано имя устройства")
		return
	}

	snap, err := s.store.Snapshot(r.Context())
	if err != nil {
		s.storeError(w, err, "Пир не найден")
		return
	}
	addr, err := allocateAddress(snap.Settings, snap.Peers)
	if err != nil {
		writeError(w, s.log, http.StatusConflict, codeConflict, err.Error())
		return
	}
	keys, err := newPeerKeys()
	if err != nil {
		s.log.Error("генерация ключей пира", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}

	created, err := s.store.CreatePeer(r.Context(), store.Peer{
		Name:         strings.TrimSpace(req.Name),
		PublicKey:    keys.public,
		PrivateKey:   keys.private,
		PresharedKey: keys.preshared,
		Address:      addr,
		Enabled:      true,
		CreatedAt:    s.now().UTC(),
	})
	if err != nil {
		s.storeError(w, err, "Пир не найден")
		return
	}
	writeJSON(w, s.log, http.StatusCreated, newPeerResponse(created))
}

// updatePeerRequest — тело `PATCH /api/peers/{id}`. Меняются только имя и признак
// включения: адрес и ключи пира менять нельзя, это выдача нового конфига.
type updatePeerRequest struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
}

// handleUpdatePeer — `PATCH /api/peers/{id}`.
func (s *Server) handleUpdatePeer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	var req updatePeerRequest
	if !s.decodeBody(w, r, &req) {
		return
	}

	p, err := s.store.Peer(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Пир не найден")
		return
	}
	if req.Name != nil {
		p.Name = strings.TrimSpace(*req.Name)
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if err := s.store.UpdatePeer(r.Context(), p); err != nil {
		s.storeError(w, err, "Пир не найден")
		return
	}
	writeJSON(w, s.log, http.StatusOK, newPeerResponse(p))
}

// handleDeletePeer — `DELETE /api/peers/{id}`. Ссылки на пира в правилах чистит store.
func (s *Server) handleDeletePeer(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	if err := s.store.DeletePeer(r.Context(), id); err != nil {
		s.storeError(w, err, "Пир не найден")
		return
	}
	writeJSON(w, s.log, http.StatusOK, map[string]bool{"ok": true})
}

// handlePeerConfig — `GET /api/peers/{id}/config`, клиентский `.conf` текстом.
//
// QR-код с сервера не отдаётся: конфиг это текст, а рисование его в картинку —
// работа клиента (см. tasks/25-api-data.md).
func (s *Server) handlePeerConfig(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	p, err := s.store.Peer(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Пир не найден")
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки не найдены")
		return
	}
	serverKey, err := s.serverPublicKey(r)
	if err != nil {
		s.log.Error("публичный ключ сервера", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}

	conf, err := clientConfig(p, settings, serverKey)
	if err != nil {
		// Конфиг без ключа сервера или без адреса подключения выглядит рабочим,
		// но не подключается. Лучше внятный отказ, чем такой файл на телефоне.
		writeError(w, s.log, http.StatusServiceUnavailable, codeNotReady, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", confFileName(p.Name)))
	if _, err := w.Write([]byte(conf)); err != nil {
		s.log.Debug("конфиг не дописан", "ошибка", err)
	}
}

// clientConfig собирает клиентский конфиг по docs/03-networking.md.
//
// Все поля фиксированы и не настраиваются на уровне пира: DNS держит всю
// селективность (docs/04-dns-fakeip.md), MTU обязан совпадать с MTU wg0
// (ADR 0004), AllowedIPs включает ::/0 без выдачи IPv6-адреса намеренно,
// PersistentKeepalive нужен мобильному клиенту за NAT.
func clientConfig(p store.Peer, s store.Settings, serverPublicKey string) (string, error) {
	var missing []string
	if serverPublicKey == "" {
		missing = append(missing, "публичный ключ сервера")
	}
	if s.EndpointHost == "" {
		missing = append(missing, "адрес сервера для подключения")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("конфиг пока не выдать: не задан %s", strings.Join(missing, ", "))
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", p.PrivateKey)
	fmt.Fprintf(&b, "Address    = %s/32\n", p.Address)
	fmt.Fprintf(&b, "DNS        = %s\n", s.WGServerAddress)
	fmt.Fprintf(&b, "MTU        = %d\n", s.ClientMTU)
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey           = %s\n", serverPublicKey)
	fmt.Fprintf(&b, "PresharedKey        = %s\n", p.PresharedKey)
	fmt.Fprintf(&b, "Endpoint            = %s\n", net.JoinHostPort(s.EndpointHost, strconv.Itoa(s.WGListenPort)))
	b.WriteString("AllowedIPs          = 0.0.0.0/0, ::/0\n")
	b.WriteString("PersistentKeepalive = 25\n")
	return b.String(), nil
}

// confFileName — имя файла для скачивания. Всё, кроме букв, цифр и дефиса,
// заменяется дефисом: имя пира вводит пользователь и в нём бывает что угодно.
func confFileName(name string) string {
	var b strings.Builder
	prev := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r >= 'а' && r <= 'я', r == 'ё':
			b.WriteRune(r)
			prev = false
		default:
			if !prev && b.Len() > 0 {
				b.WriteByte('-')
				prev = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "peer"
	}
	return "razdacha-" + out + ".conf"
}

// peerKeys — комплект ключей одного пира.
type peerKeys struct {
	private   string
	public    string
	preshared string
}

// newPeerKeys выдаёт ключи пира. Формат тот же, что у wg(8): 32 байта в base64,
// приватный ключ «зажат» по X25519.
func newPeerKeys() (peerKeys, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return peerKeys{}, fmt.Errorf("генерация приватного ключа: %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return peerKeys{}, fmt.Errorf("вычисление публичного ключа: %w", err)
	}

	var psk [32]byte
	if _, err := rand.Read(psk[:]); err != nil {
		return peerKeys{}, fmt.Errorf("генерация pre-shared ключа: %w", err)
	}

	enc := base64.StdEncoding
	return peerKeys{
		private:   enc.EncodeToString(priv[:]),
		public:    enc.EncodeToString(pub),
		preshared: enc.EncodeToString(psk[:]),
	}, nil
}

// allocateAddress выбирает первый свободный адрес пула. Адрес сервера, адрес сети
// и широковещательный пропускаются.
func allocateAddress(s store.Settings, peers []store.Peer) (string, error) {
	pool, err := netip.ParsePrefix(s.WGPool)
	if err != nil {
		return "", fmt.Errorf("%w: пул адресов %q не разобран", ErrNoAddress, s.WGPool)
	}
	pool = pool.Masked()

	taken := make(map[netip.Addr]bool, len(peers)+1)
	for _, p := range peers {
		if a, err := netip.ParseAddr(p.Address); err == nil {
			taken[a] = true
		}
	}
	if a, err := netip.ParseAddr(s.WGServerAddress); err == nil {
		taken[a] = true
	}

	// Первый адрес сети сам по себе не выдаётся; последний в IPv4-подсети —
	// широковещательный, его тоже пропускаем.
	last := broadcast(pool)
	for a := pool.Addr().Next(); pool.Contains(a); a = a.Next() {
		if a == last {
			break
		}
		if !taken[a] {
			return a.String(), nil
		}
	}
	return "", ErrNoAddress
}

// broadcast — последний адрес подсети.
func broadcast(p netip.Prefix) netip.Addr {
	octets := p.Addr().AsSlice()
	bits := p.Bits()
	for i := range octets {
		shift := bits - i*8
		switch {
		case shift <= 0:
			octets[i] = 0xff
		case shift < 8:
			octets[i] |= byte(0xff >> shift)
		}
	}
	addr, _ := netip.AddrFromSlice(octets)
	return addr
}
