package packaging

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const ingressDropIn = "50-razdacha-ingress.conf"

// CheckSingboxIngressOverrides refuses existing later overrides that could
// replace the exact managed config source or run after/remove its startup guard.
// It reads supported systemd drop-in locations and never changes foreign files.
func (u *UnitInstaller) CheckSingboxIngressOverrides() error {
	roots := []string{u.dir(), "/run/systemd/system", "/usr/local/lib/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system", "/etc/systemd/system.control", "/run/systemd/system.control"}
	roots = append(roots, u.PackagedDirs...)
	seen := map[string]bool{}
	for _, root := range roots {
		for _, scope := range []string{SingboxUnit + ".d", "sing-.service.d", "service.d"} {
			directory := filepath.Join(u.Root, root, scope)
			if seen[directory] {
				continue
			}
			seen[directory] = true
			files, err := os.ReadDir(directory)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("inspect runtime drop-ins: %w", err)
			}
			for _, file := range files {
				if file.IsDir() || !strings.HasSuffix(file.Name(), ".conf") || file.Name() < ingressDropIn || (file.Name() == ingressDropIn && directory == u.Path(SingboxUnit+".d")) {
					continue
				}
				path := filepath.Join(directory, file.Name())
				if err := checkIngressOverride(path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkIngressOverride(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read runtime drop-in: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	section := ""
	checkLine := func(line string) error {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			return nil
		}
		if section != "Service" {
			return nil
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			return nil
		}
		switch strings.TrimSpace(key) {
		case "ExecStart", "ExecStartPre":
			return fmt.Errorf("%w: runtime drop-in %s conflicts with the managed ingress command or guard; adjust it before setup", ErrForeignConfig, path)
		}
		return nil
	}
	var logical strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		continued := strings.HasSuffix(line, "\\")
		if continued {
			line = strings.TrimSuffix(line, "\\")
		}
		if logical.Len()+len(line)+1 > 1<<20 {
			return fmt.Errorf("runtime drop-in %s logical line exceeds size limit", path)
		}
		if logical.Len() > 0 {
			logical.WriteByte(' ')
		}
		logical.WriteString(line)
		if continued {
			continue
		}
		if err := checkLine(strings.TrimSpace(logical.String())); err != nil {
			return err
		}
		logical.Reset()
	}
	if logical.Len() > 0 {
		if err := checkLine(strings.TrimSpace(logical.String())); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("inspect runtime drop-in %s: %w", path, err)
	}
	return nil
}
