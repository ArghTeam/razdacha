package store

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const testPhrase = "фраза подлиннее"

// Копия шифруется и расшифровывается той же фразой — иначе бэкап бесполезен
// ровно тогда, когда он нужен.
func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte(sqliteMagic + "состояние razdacha")

	enc, err := EncryptBackup(plain, testPhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	if bytes.Contains(enc, []byte("состояние razdacha")) {
		t.Fatal("исходные байты видны в зашифрованном файле")
	}
	if !IsEncryptedBackup(enc) {
		t.Error("зашифрованная копия не узнаётся по магии")
	}
	if IsStateFile(enc) {
		t.Error("зашифрованная копия выдаёт себя за файл SQLite")
	}

	got, err := DecryptBackup(enc, testPhrase)
	if err != nil {
		t.Fatalf("DecryptBackup: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Error("расшифрованное не совпало с исходным")
	}
	if !IsStateFile(got) {
		t.Error("расшифрованное не похоже на файл SQLite")
	}
}

// Соль и nonce случайны: две копии одного состояния не совпадают байт в байт,
// и по их сравнению нельзя сказать, менялось ли что-нибудь.
func TestEncryptBackupIsSalted(t *testing.T) {
	plain := []byte(sqliteMagic + "одно и то же")

	first, err := EncryptBackup(plain, testPhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	second, err := EncryptBackup(plain, testPhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Error("две копии одного состояния совпали побайтно")
	}
}

// Неверная фраза не расшифровывает и не выдаёт мусор за состояние.
func TestDecryptWrongPassphrase(t *testing.T) {
	enc, err := EncryptBackup([]byte(sqliteMagic+"состояние"), testPhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}

	_, err = DecryptBackup(enc, "совсем другая фраза")
	if !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("DecryptBackup с чужой фразой = %v, ожидалась ErrBadPassphrase", err)
	}
	// В тексте ошибки не должно быть ни фразы, ни намёка на её длину.
	if strings.Contains(err.Error(), "совсем другая фраза") {
		t.Error("фраза попала в текст ошибки")
	}
}

// Подмена байтов ловится GCM: битый файл не превращается в «восстановленное
// состояние», а отвергается.
func TestDecryptDetectsTampering(t *testing.T) {
	enc, err := EncryptBackup([]byte(sqliteMagic+"состояние"), testPhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}

	t.Run("шифротекст", func(t *testing.T) {
		bad := bytes.Clone(enc)
		bad[len(bad)-1] ^= 0xFF
		if _, err := DecryptBackup(bad, testPhrase); !errors.Is(err, ErrBadPassphrase) {
			t.Fatalf("подмена шифротекста = %v", err)
		}
	})

	t.Run("параметры KDF в заголовке", func(t *testing.T) {
		bad := bytes.Clone(enc)
		// Младший байт параметра t: заголовок это четыре байта big-endian сразу
		// за магией и идентификатором KDF. Портится именно младший — старший
		// дал бы 16 миллионов проходов argon2, то есть тест длиной в вечность.
		bad[len(backupMagic)+1+3] ^= 0x01
		if _, err := DecryptBackup(bad, testPhrase); err == nil {
			t.Fatal("подмена параметров KDF прошла незамеченной")
		}
	})

	t.Run("завышенные параметры KDF", func(t *testing.T) {
		bad := bytes.Clone(enc)
		// Старший байт t: без потолка восстановление считало бы ключ часами,
		// и подменённый заголовок стоил бы владельцу сервера дороже отказа.
		bad[len(backupMagic)+1] = 0xFF
		if _, err := DecryptBackup(bad, testPhrase); !errors.Is(err, ErrNotBackup) {
			t.Fatalf("завышенный параметр KDF = %v, ожидалась ErrNotBackup", err)
		}
	})
}

// Файл, который не наш и не SQLite, отвергается до вопроса о фразе.
func TestDecryptRejectsForeignFile(t *testing.T) {
	_, err := DecryptBackup([]byte("это просто текст"), testPhrase)
	if !errors.Is(err, ErrNotBackup) {
		t.Fatalf("чужой файл = %v, ожидалась ErrNotBackup", err)
	}

	if IsEncryptedBackup([]byte("это")) || IsStateFile([]byte("это")) {
		t.Error("короткая строка опознана как копия")
	}
	if !IsStateFile([]byte(sqliteMagic + "прочее")) {
		t.Error("файл SQLite не узнан")
	}
}

// Обрезанный контейнер — не повод паниковать на срезе заголовка.
func TestDecryptTruncated(t *testing.T) {
	enc, err := EncryptBackup([]byte(sqliteMagic+"состояние"), testPhrase)
	if err != nil {
		t.Fatalf("EncryptBackup: %v", err)
	}
	if _, err := DecryptBackup(enc[:len(backupMagic)+3], testPhrase); !errors.Is(err, ErrNotBackup) {
		t.Fatalf("обрезанный файл = %v, ожидалась ErrNotBackup", err)
	}
}

func TestEncryptRequiresPassphrase(t *testing.T) {
	if _, err := EncryptBackup([]byte(sqliteMagic), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("шифрование без фразы = %v, ожидалась ErrInvalid", err)
	}
}
