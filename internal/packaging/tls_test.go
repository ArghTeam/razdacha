package packaging

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение сертификата: %v", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("сертификат не в PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("разбор сертификата: %v", err)
	}
	return cert
}

func TestEnsureCertificateIssues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	ip := net.ParseIP(DefaultPanelAddr)

	paths, issued, err := EnsureCertificate(dir, []net.IP{ip}, now)
	if err != nil {
		t.Fatalf("EnsureCertificate: %v", err)
	}
	if !issued {
		t.Fatal("сертификата не было, а issued = false")
	}

	cert := readCert(t, paths.CertFile)
	if !containsIP(cert.IPAddresses, ip) {
		t.Errorf("SAN не содержит %s: %v", ip, cert.IPAddresses)
	}
	if len(cert.DNSNames) != 0 {
		t.Errorf("в сертификате появились DNS-имена: %v", cert.DNSNames)
	}
	if got := cert.NotAfter.Sub(now); got < 9*365*24*time.Hour {
		t.Errorf("срок сертификата %v, ожидались годы", got)
	}
	if !now.After(cert.NotBefore) {
		t.Error("NotBefore не сдвинут назад: расхождение часов сделает сертификат недействительным")
	}
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		t.Errorf("ключ не ECDSA: %v", cert.PublicKeyAlgorithm)
	}
}

func TestEnsureCertificateFileModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	paths, _, err := EnsureCertificate(dir, []net.IP{net.ParseIP(DefaultPanelAddr)}, time.Now())
	if err != nil {
		t.Fatalf("EnsureCertificate: %v", err)
	}

	keyInfo, err := os.Stat(paths.KeyFile)
	if err != nil {
		t.Fatalf("stat ключа: %v", err)
	}
	if got := keyInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("права ключа %o, ожидались 0600", got)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat каталога: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("права каталога TLS %o, ожидались 0700", got)
	}
}

// Валидный сертификат не перевыпускается: пользователь добавляет
// самоподписанный в исключения браузера руками, и смена на каждой установке
// эту работу обнуляла бы.
func TestEnsureCertificateKeepsValid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	ip := net.ParseIP(DefaultPanelAddr)

	paths, _, err := EnsureCertificate(dir, []net.IP{ip}, now)
	if err != nil {
		t.Fatalf("первый выпуск: %v", err)
	}
	before := readCert(t, paths.CertFile)

	_, issued, err := EnsureCertificate(dir, []net.IP{ip}, now.Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("повторный вызов: %v", err)
	}
	if issued {
		t.Fatal("валидный сертификат перевыпущен")
	}
	if after := readCert(t, paths.CertFile); after.SerialNumber.Cmp(before.SerialNumber) != 0 {
		t.Fatal("серийный номер изменился: сертификат подменён")
	}
}

func TestEnsureCertificateReissues(t *testing.T) {
	ip := net.ParseIP(DefaultPanelAddr)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

	cases := map[string]struct {
		at    time.Time
		ips   []net.IP
		spoil func(t *testing.T, p CertPaths)
	}{
		"истёк": {
			at:  now.Add(certLifetime),
			ips: []net.IP{ip},
		},
		"нет нужного адреса в SAN": {
			at:  now,
			ips: []net.IP{net.ParseIP("10.9.0.1")},
		},
		// Переход в публичный режим: адрес VPN в SAN есть, внешнего нет.
		"добавился внешний адрес": {
			at:  now,
			ips: []net.IP{ip, net.ParseIP("203.0.113.10")},
		},
		"ключ пропал": {
			at:    now,
			ips:   []net.IP{ip},
			spoil: func(t *testing.T, p CertPaths) { t.Helper(); os.Remove(p.KeyFile) },
		},
		"сертификат битый": {
			at:  now,
			ips: []net.IP{ip},
			spoil: func(t *testing.T, p CertPaths) {
				t.Helper()
				os.WriteFile(p.CertFile, []byte("не PEM"), 0o644)
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")
			paths, _, err := EnsureCertificate(dir, []net.IP{ip}, now)
			if err != nil {
				t.Fatalf("первый выпуск: %v", err)
			}
			if tc.spoil != nil {
				tc.spoil(t, paths)
			}
			if _, issued, err := EnsureCertificate(dir, tc.ips, tc.at); err != nil || !issued {
				t.Fatalf("ожидался перевыпуск, получено issued=%v err=%v", issued, err)
			}
		})
	}
}

func TestEnsureCertificateRejectsEmptyInput(t *testing.T) {
	if _, _, err := EnsureCertificate("", []net.IP{net.ParseIP(DefaultPanelAddr)}, time.Now()); err == nil {
		t.Error("пустой каталог принят")
	}
	if _, _, err := EnsureCertificate(t.TempDir(), nil, time.Now()); err == nil {
		t.Error("пустой список адресов принят")
	}
}
