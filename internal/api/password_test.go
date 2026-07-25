package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

const testPassword = "правильный-пароль-1"

// Один и тот же пароль даёт разные хеши: соль случайна на каждый вызов.
func TestHashPasswordSalted(t *testing.T) {
	first, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Fatal("два хеша одного пароля совпали — соль не работает")
	}
	if strings.Contains(first, testPassword) {
		t.Fatal("пароль виден в хеше")
	}
	if !strings.HasPrefix(first, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("неожиданный формат хеша: %q", first)
	}

	for _, encoded := range []string{first, second} {
		ok, err := VerifyPassword(encoded, testPassword)
		if err != nil {
			t.Fatalf("VerifyPassword: %v", err)
		}
		if !ok {
			t.Error("верный пароль не принят")
		}
	}
}

func TestVerifyPasswordRejects(t *testing.T) {
	encoded, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	for _, wrong := range []string{"", "правильный-пароль-2", "ПРАВИЛЬНЫЙ-ПАРОЛЬ-1", testPassword + " "} {
		ok, err := VerifyPassword(encoded, wrong)
		if err != nil {
			t.Fatalf("VerifyPassword(%q): %v", wrong, err)
		}
		if ok {
			t.Errorf("принят неверный пароль %q", wrong)
		}
	}
}

func TestHashPasswordRejectsShort(t *testing.T) {
	if _, err := HashPassword("короткий"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("ожидалась ErrWeakPassword, получено: %v", err)
	}
}

func TestVerifyPasswordBadHash(t *testing.T) {
	cases := map[string]string{
		"пустой":            "",
		"не PHC":            "просто строка",
		"чужой алгоритм":    "$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"битая база":        "$argon2id$v=19$m=65536,t=3,p=4$!!!$aGFzaA",
		"нулевые параметры": "$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",
	}
	for name, encoded := range cases {
		if _, err := VerifyPassword(encoded, testPassword); !errors.Is(err, ErrBadHash) {
			t.Errorf("%s: ожидалась ErrBadHash, получено: %v", name, err)
		}
	}
}

// Параметры читаются из самой строки, а не из констант: их ужесточение не
// должно ломать вход по уже сохранённому паролю.
func TestVerifyPasswordUsesStoredParams(t *testing.T) {
	// Заведомо не наши параметры: m=8 KiB, t=1, p=1.
	salt := []byte("соль-16-байтов")
	sum := argon2.IDKey([]byte(testPassword), salt, 1, 8, 1, argonKeyLen)
	b64 := base64.RawStdEncoding
	encoded := fmt.Sprintf("$argon2id$v=19$m=8,t=1,p=1$%s$%s",
		b64.EncodeToString(salt), b64.EncodeToString(sum))

	ok, err := VerifyPassword(encoded, testPassword)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("хеш со старыми параметрами не принят")
	}
}
