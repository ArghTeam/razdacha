package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// Формат зашифрованной копии (docs/08-install-upgrade.md#формат-зашифрованной-копии).
//
// Своего формата не хотелось, но готового в зависимостях нет, а тянуть age или
// openssl ради одного файла — это либо новая зависимость, либо вызов бинарника,
// запрещённый конституцией. Здесь только stdlib и argon2id из
// `golang.org/x/crypto`, откуда его уже берёт пароль панели.
//
// Раскладка:
//
//	"RZDBAK1\n"  8   магия: по ней файл отличается от обычной копии SQLite
//	kdf          1   идентификатор KDF, пока один
//	time         4   параметр argon2id t, big-endian
//	memory       4   параметр argon2id m в KiB, big-endian
//	threads      1   параметр argon2id p
//	соль        16
//	nonce       12
//	шифротекст   —   AES-256-GCM, дополнительные данные — весь заголовок
//
// Параметры KDF лежат в файле, а не в константах: ужесточение параметров не
// должно ломать расшифровку прежних копий — тот же приём, что у пароля панели.
// Заголовок целиком уходит в дополнительные данные GCM, поэтому подкрутить их
// снаружи, чтобы расшифровка считала ключ дешевле, нельзя.
const (
	backupMagic  = "RZDBAK1\n"
	backupKDFID  = 1
	backupHeader = len(backupMagic) + 1 + 4 + 4 + 1 + backupSaltLen + backupNonceLen

	backupSaltLen  = 16
	backupNonceLen = 12
	backupKeyLen   = 32 // AES-256
)

// Потолки параметров KDF из заголовка. Они не про стойкость, а про то, чтобы
// чужой файл не заставил восстановление считать ключ часами и брать под него
// гигабайты: 16 проходов по 1 ГиБ — заведомо больше всего, что имеет смысл.
const (
	maxKDFTime    uint32 = 16
	maxKDFMemory  uint32 = 1 << 20 // KiB, то есть 1 ГиБ
	maxKDFThreads uint8  = 16
)

// sqliteMagic — заголовок файла SQLite. Обычная копия начинается с него, наша
// зашифрованная — с [backupMagic]; по расширению файла никто не гадает.
const sqliteMagic = "SQLite format 3\x00"

// ErrBadPassphrase — расшифровать не удалось. Неверная фраза и повреждённый
// файл здесь неразличимы по построению: GCM отвечает на оба одинаково, и
// утверждать что-то одно значило бы соврать.
var ErrBadPassphrase = errors.New("парольная фраза не подходит или файл повреждён")

// ErrNotBackup — файл не похож ни на нашу копию, ни на базу SQLite.
var ErrNotBackup = errors.New("файл не похож на копию состояния razdacha")

// IsEncryptedBackup отвечает, наш ли это зашифрованный контейнер.
func IsEncryptedBackup(data []byte) bool {
	return len(data) >= len(backupMagic) && string(data[:len(backupMagic)]) == backupMagic
}

// IsStateFile отвечает, файл ли это SQLite — то есть незашифрованная копия.
func IsStateFile(data []byte) bool {
	return len(data) >= len(sqliteMagic) && string(data[:len(sqliteMagic)]) == sqliteMagic
}

// EncryptBackup шифрует копию парольной фразой.
//
// Фраза в ошибки не попадает ни при каком исходе: тексты отсюда доезжают и до
// панели, и до лога.
func EncryptBackup(data []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("%w: парольная фраза не задана", ErrInvalid)
	}

	salt := make([]byte, backupSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("генерация соли: %w", err)
	}
	nonce := make([]byte, backupNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("генерация nonce: %w", err)
	}

	head := header(argonTime, argonMemory, argonThreads, salt, nonce)
	gcm, err := aead(passphrase, salt, argonTime, argonMemory, argonThreads)
	if err != nil {
		return nil, err
	}
	// Заголовок остаётся в начале файла и он же — дополнительные данные.
	return gcm.Seal(head, nonce, data, head), nil
}

// DecryptBackup расшифровывает контейнер парольной фразой.
func DecryptBackup(data []byte, passphrase string) ([]byte, error) {
	if !IsEncryptedBackup(data) {
		return nil, ErrNotBackup
	}
	if len(data) < backupHeader {
		return nil, fmt.Errorf("%w: файл обрезан", ErrNotBackup)
	}
	head := data[:backupHeader]

	pos := len(backupMagic)
	if head[pos] != backupKDFID {
		return nil, fmt.Errorf("%w: копия сделана незнакомым способом (KDF %d)",
			ErrNotBackup, head[pos])
	}
	pos++
	t := binary.BigEndian.Uint32(head[pos : pos+4])
	pos += 4
	m := binary.BigEndian.Uint32(head[pos : pos+4])
	pos += 4
	p := head[pos]
	pos++
	salt := head[pos : pos+backupSaltLen]
	pos += backupSaltLen
	nonce := head[pos : pos+backupNonceLen]

	// Параметры приходят из файла, а файл приходит откуда угодно — из чата, с
	// флешки, от постороннего. Нулевые роняют argon2 паникой, а завышенные
	// превращают восстановление в вечный цикл на гигабайтах памяти: подменённый
	// заголовок не должен стоить владельцу сервера. Потолки выбраны с запасом
	// к нашим параметрам (t=3, m=64 MiB, p=4) — чужие копии с более жёсткими
	// настройками в этих границах читаются.
	if t == 0 || m == 0 || p == 0 {
		return nil, fmt.Errorf("%w: параметры шифрования испорчены", ErrNotBackup)
	}
	if t > maxKDFTime || m > maxKDFMemory || p > maxKDFThreads {
		return nil, fmt.Errorf("%w: параметры шифрования вне разумных границ", ErrNotBackup)
	}

	gcm, err := aead(passphrase, salt, t, m, p)
	if err != nil {
		return nil, err
	}
	out, err := gcm.Open(nil, nonce, data[backupHeader:], head)
	if err != nil {
		return nil, ErrBadPassphrase
	}
	return out, nil
}

// header собирает заголовок контейнера.
func header(t, m uint32, p uint8, salt, nonce []byte) []byte {
	out := make([]byte, 0, backupHeader)
	out = append(out, backupMagic...)
	out = append(out, backupKDFID)
	out = binary.BigEndian.AppendUint32(out, t)
	out = binary.BigEndian.AppendUint32(out, m)
	out = append(out, p)
	out = append(out, salt...)
	out = append(out, nonce...)
	return out
}

// aead считает ключ из фразы и собирает AES-256-GCM.
func aead(passphrase string, salt []byte, t, m uint32, p uint8) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), salt, t, m, p, backupKeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("сборка шифра: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("сборка шифра: %w", err)
	}
	return gcm, nil
}

// Параметры argon2id для копии — те же, что у пароля панели (RFC 9106, второй
// рекомендованный набор). Дублируются здесь, а не берутся из слоя api: слой
// хранения на него не ссылается, а расходиться им не даёт то, что оба набора
// объявлены константами и записаны в сам файл.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
)
