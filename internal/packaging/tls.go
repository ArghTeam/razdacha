package packaging

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// CertFileName и KeyFileName — имена файлов в каталоге TLS.
	CertFileName = "cert.pem"
	KeyFileName  = "key.pem"

	// certLifetime — срок сертификата. Десять лет: самоподписанный
	// сертификат обновлять нечем, а истекший сломал бы панель на машине,
	// куда никто не заходит годами.
	certLifetime = 10 * 365 * 24 * time.Hour

	// certRenewBefore — за сколько до конца срока сертификат считается
	// негодным и выпускается заново.
	certRenewBefore = 30 * 24 * time.Hour

	// certBackdate компенсирует расхождение часов: на свежем VPS время
	// нередко уезжает на минуты, и сертификат «из будущего» браузер
	// отвергает.
	certBackdate = time.Hour
)

// CertPaths — где лежат сертификат и ключ панели.
type CertPaths struct {
	CertFile string
	KeyFile  string
}

// EnsureCertificate возвращает пути к сертификату панели, выпуская его при
// необходимости. Второе значение — был ли сертификат выпущен сейчас.
//
// Существующий сертификат не перевыпускается, если он разбирается, не истекает
// в ближайший месяц, покрывает все нужные адреса и его ключ лежит рядом:
// пользователь добавляет самоподписанный сертификат в исключения браузера
// вручную, и смена его на каждой установке эту работу обнуляла бы.
func EnsureCertificate(dir string, ips []net.IP, now time.Time) (CertPaths, bool, error) {
	if dir == "" {
		return CertPaths{}, false, fmt.Errorf("%w: не задан каталог TLS", ErrBadCertificate)
	}
	if len(ips) == 0 {
		return CertPaths{}, false, fmt.Errorf("%w: не заданы адреса для SAN", ErrBadCertificate)
	}

	paths := CertPaths{
		CertFile: filepath.Join(dir, CertFileName),
		KeyFile:  filepath.Join(dir, KeyFileName),
	}

	if certUsable(paths, ips, now) {
		return paths, false, nil
	}

	// 0700: рядом лежит приватный ключ панели, расширять доступ на каталог
	// незачем.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return CertPaths{}, false, fmt.Errorf("создание каталога TLS %s: %w", dir, err)
	}
	if err := issueCertificate(paths, ips, now); err != nil {
		return CertPaths{}, false, err
	}
	return paths, true, nil
}

// certUsable отвечает, годится ли уже лежащий на диске сертификат.
//
// Любая причина, по которой прочитать его не удалось, — повод выпустить новый,
// а не ошибка: на чистой машине файлов нет вовсе, а нечитаемый файл всё равно
// будет перезаписан, и там же вылезет внятная ошибка записи.
func certUsable(paths CertPaths, ips []net.IP, now time.Time) bool {
	certPEM, err := os.ReadFile(paths.CertFile)
	if err != nil {
		return false
	}
	if _, err := os.Stat(paths.KeyFile); err != nil {
		// Сертификат без ключа бесполезен: nginx с ним не поднимется.
		return false
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if now.Before(cert.NotBefore) || now.Add(certRenewBefore).After(cert.NotAfter) {
		return false
	}
	for _, ip := range ips {
		if !containsIP(cert.IPAddresses, ip) {
			return false
		}
	}
	return true
}

func containsIP(have []net.IP, want net.IP) bool {
	for _, ip := range have {
		if ip.Equal(want) {
			return true
		}
	}
	return false
}

// issueCertificate выпускает самоподписанный сертификат средствами Go.
// Внешний openssl не вызывается: конституция требует библиотеку там, где она
// есть, а на минимальном образе Debian openssl может и не оказаться.
func issueCertificate(paths CertPaths, ips []net.IP, now time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("генерация ключа панели: %w", err)
	}

	serialMax := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialMax)
	if err != nil {
		return fmt.Errorf("генерация серийного номера сертификата: %w", err)
	}

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   ips[0].String(),
			Organization: []string{"razdacha"},
		},
		NotBefore:             now.Add(-certBackdate),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("выпуск сертификата панели: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("сериализация ключа панели: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	// Ключ пишется первым и строго 0600: файл, который на мгновение полежал
	// с широкими правами, считается скомпрометированным.
	if err := writeFile(paths.KeyFile, keyPEM, 0o600); err != nil {
		return fmt.Errorf("запись ключа панели: %w", err)
	}
	if err := writeFile(paths.CertFile, certPEM, 0o644); err != nil {
		return fmt.Errorf("запись сертификата панели: %w", err)
	}
	return nil
}
