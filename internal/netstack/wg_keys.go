package netstack

import (
	"context"
	"fmt"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// WGKeyStore — хранение приватного ключа сервера. Ключ лежит в той же БД, что и
// остальное состояние: у неё уже права 0600 и в ней уже лежат приватные ключи
// пиров, второе место хранения секретов заводить незачем.
//
// Пустая строка от [WGKeyStore.ServerPrivateKey] означает «ключа ещё нет».
type WGKeyStore interface {
	ServerPrivateKey(ctx context.Context) (string, error)
	SetServerPrivateKey(ctx context.Context, key string) error
}

// EnsureWGServerKey возвращает приватный ключ сервера, создавая его при первом
// запуске. Повторный вызов отдаёт тот же ключ: смена ключа сервера означает
// перевыпуск всех клиентских конфигов и делается только явно.
func EnsureWGServerKey(ctx context.Context, ks WGKeyStore) (wgtypes.Key, error) {
	if ks == nil {
		return wgtypes.Key{}, fmt.Errorf("%w: не задано хранилище ключей", ErrWGConfig)
	}
	stored, err := ks.ServerPrivateKey(ctx)
	if err != nil {
		return wgtypes.Key{}, fmt.Errorf("чтение приватного ключа сервера: %w", err)
	}
	if stored != "" {
		key, err := wgtypes.ParseKey(stored)
		if err != nil {
			return wgtypes.Key{}, fmt.Errorf("%w: приватный ключ сервера не разобран: %w",
				ErrWGConfig, err)
		}
		return key, nil
	}

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return wgtypes.Key{}, fmt.Errorf("генерация приватного ключа сервера: %w", err)
	}
	if err := ks.SetServerPrivateKey(ctx, key.String()); err != nil {
		return wgtypes.Key{}, fmt.Errorf("сохранение приватного ключа сервера: %w", err)
	}
	return key, nil
}

// WGServerPublicKey отдаёт публичный ключ сервера для клиентских конфигов.
//
// Ключ здесь не создаётся: пустая строка означает, что интерфейс ни разу не
// поднимали, и API честно отдаёт `server_public_key: null`. Генерация по запросу
// из панели выдала бы ключ, которого нет ни на одном интерфейсе.
func WGServerPublicKey(ctx context.Context, ks WGKeyStore) (string, error) {
	if ks == nil {
		return "", nil
	}
	stored, err := ks.ServerPrivateKey(ctx)
	if err != nil {
		return "", fmt.Errorf("чтение приватного ключа сервера: %w", err)
	}
	if stored == "" {
		return "", nil
	}
	key, err := wgtypes.ParseKey(stored)
	if err != nil {
		return "", fmt.Errorf("%w: приватный ключ сервера не разобран: %w", ErrWGConfig, err)
	}
	return key.PublicKey().String(), nil
}
