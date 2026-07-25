package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Параметры argon2id. Взяты из второго рекомендованного набора RFC 9106
// (t=3, m=64 MiB, p=4): вариант на 2 GiB памяти на VPS с 512 MiB не живёт, а
// более слабый набор дешевле перебирать на GPU.
//
// Общая работа не зависит от p — четыре полосы на одном ядре просто считаются
// по очереди, — поэтому p оставлен рекомендованным. Память в 64 MiB на проверку
// это ещё и вектор отказа: одновременные проверки ограничены семафором в
// [Server], а частота попыток — блокировкой по адресу.
//
// Параметры пишутся в сам хеш, поэтому их изменение не ломает старые пароли:
// проверка считает по параметрам из строки, а не по этим константам.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// MinPasswordLen — минимальная длина пароля панели. Пароль генерирует
// установщик, и он длиннее; ограничение отсекает ручную замену на «12345678»
// при смене пароля из UI. Панель торчит в интернет, короткий пароль там
// подбирается быстрее, чем владелец успевает это заметить.
const MinPasswordLen = 12

// HashPassword считает argon2id-хеш пароля со случайной солью и возвращает его
// в формате PHC: `$argon2id$v=19$m=...,t=...,p=...$<соль>$<хеш>`.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLen {
		return "", fmt.Errorf("%w: минимум %d символов", ErrWeakPassword, MinPasswordLen)
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("генерация соли: %w", err)
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(sum)), nil
}

// VerifyPassword проверяет пароль против закодированного хеша. Сравнение
// постоянное по времени; ошибка означает нечитаемый хеш, а не неверный пароль.
func VerifyPassword(encoded, password string) (bool, error) {
	salt, want, params, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// SetPassword хеширует пароль и сохраняет хеш. Открытый пароль дальше этой
// функции не уходит: в БД, логи и ошибки попадает только хеш.
func SetPassword(ctx context.Context, st *store.Store, password string) error {
	encoded, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := st.SetPasswordHash(ctx, encoded); err != nil {
		return fmt.Errorf("сохранение пароля панели: %w", err)
	}
	return nil
}

// argonParams — параметры, прочитанные из строки хеша.
type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

// decodeHash разбирает строку формата PHC.
func decodeHash(encoded string) (salt, sum []byte, params argonParams, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, nil, params, fmt.Errorf("%w: не формат argon2id", ErrBadHash)
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, params, fmt.Errorf("%w: версия: %w", ErrBadHash, err)
	}
	if version != argon2.Version {
		return nil, nil, params, fmt.Errorf("%w: версия argon2 %d не поддерживается", ErrBadHash, version)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.time, &params.threads); err != nil {
		return nil, nil, params, fmt.Errorf("%w: параметры: %w", ErrBadHash, err)
	}
	if params.memory == 0 || params.time == 0 || params.threads == 0 {
		return nil, nil, params, fmt.Errorf("%w: нулевые параметры", ErrBadHash)
	}

	b64 := base64.RawStdEncoding
	salt, err = b64.DecodeString(parts[4])
	if err != nil {
		return nil, nil, params, fmt.Errorf("%w: соль: %w", ErrBadHash, err)
	}
	sum, err = b64.DecodeString(parts[5])
	if err != nil {
		return nil, nil, params, fmt.Errorf("%w: хеш: %w", ErrBadHash, err)
	}
	if len(salt) == 0 || len(sum) == 0 {
		return nil, nil, params, fmt.Errorf("%w: пустая соль или хеш", ErrBadHash)
	}
	return salt, sum, params, nil
}
