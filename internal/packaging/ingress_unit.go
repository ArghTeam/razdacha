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

// CheckSingboxIngressOverrides rejects filesystem views that could make the
// runtime read different bytes from its host-side controller/privileged helper,
// and later overrides that replace the managed command or startup guard.
// It reads supported systemd base and drop-in locations and never changes foreign files.
func (u *UnitInstaller) CheckSingboxIngressOverrides() error {
	roots := []string{u.dir(), "/run/systemd/system", "/usr/local/lib/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system", "/etc/systemd/system.control", "/run/systemd/system.control"}
	roots = append(roots, u.PackagedDirs...)
	seen := map[string]bool{}
	for _, root := range roots {
		base := filepath.Join(u.Root, root, SingboxUnit)
		if _, err := os.Lstat(base); err == nil {
			if err := checkIngressUnitName(base); err != nil {
				return err
			}
			if err := checkIngressUnit(base, false); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect runtime unit: %w", err)
		}
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
				if file.IsDir() || !strings.HasSuffix(file.Name(), ".conf") {
					continue
				}
				path := filepath.Join(directory, file.Name())
				checkCommand := file.Name() > ingressDropIn || (file.Name() == ingressDropIn && directory != u.Path(SingboxUnit+".d"))
				if err := checkIngressUnit(path, checkCommand); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkIngressUnit(path string, checkCommand bool) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read runtime unit fragment: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	section := ""
	checkLine := func(line string) error {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == ".include" {
			return fmt.Errorf("%w: runtime unit %s uses an unsupported legacy include; use explicit inspected unit fragments", ErrForeignConfig, path)
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			return nil
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if section == "Unit" && key == "JoinsNamespaceOf" && value != "" {
			return fmt.Errorf("%w: runtime unit %s joins another namespace; public ingress requires the host network namespace", ErrForeignConfig, path)
		}
		if section != "Service" {
			return nil
		}
		switch key {
		case "NetworkNamespacePath":
			if value != "" {
				return fmt.Errorf("%w: runtime unit %s changes network namespace; public ingress requires host networking", ErrForeignConfig, path)
			}
		case "PrivateNetwork":
			switch strings.ToLower(value) {
			case "", "0", "no", "false", "off":
			default:
				return fmt.Errorf("%w: runtime unit %s enables unsupported private networking", ErrForeignConfig, path)
			}
		case "RootDirectory", "RootImage", "RootMStack", "BindPaths", "BindReadOnlyPaths", "TemporaryFileSystem", "MountImages", "ExtensionImages", "ExtensionDirectories":
			if strings.TrimSpace(value) != "" {
				return fmt.Errorf("%w: runtime unit %s uses unsupported filesystem remapping %s; the runtime and ingress authority must share the host file view", ErrForeignConfig, path, strings.TrimSpace(key))
			}
		case "ExecReload":
			if checkCommand && value != "" {
				return fmt.Errorf("%w: runtime unit %s restores an unsupported reload hook", ErrForeignConfig, path)
			}
		case "ExecStartPost":
			if value != "" {
				return fmt.Errorf("%w: runtime unit %s has an unsupported post-start hook", ErrForeignConfig, path)
			}
		case "ExecStart", "ExecStartPre":
			if !checkCommand {
				return nil
			}
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

// Alternate service names carry additional canonical/alias drop-in scopes outside
// this bounded contract. Ordinary same-name distribution symlinks remain valid.
func checkIngressUnitName(path string) error {
	current := path
	for range 40 {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect runtime unit link: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(current)
		if err != nil {
			return fmt.Errorf("read runtime unit link: %w", err)
		}
		if filepath.Base(target) != SingboxUnit {
			return fmt.Errorf("%w: runtime unit %s aliases another service name; use the explicit managed service name", ErrForeignConfig, path)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		current = target
	}
	return fmt.Errorf("%w: runtime unit link chain exceeds supported limit", ErrForeignConfig)
}
