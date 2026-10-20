package packaging

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ArghTeam/razdacha/internal/restorestate"
)

const RestoreDropIn = "90-razdacha-restore.conf"

// RestoreServices restricts restore to provable managed unit execution and paths.
// Query/Run are adapters so local fixtures never access host systemd.
type RestoreServices struct {
	Root  string
	Query func(context.Context, ...string) ([]byte, error)
	Run   func(context.Context, ...string) error
}
type RestoreUnitState struct{ Active, Enabled string }
type RestoreServiceState map[string]RestoreUnitState

func SystemdQuery(ctx context.Context, args ...string) ([]byte, error) {
	b, e := exec.CommandContext(ctx, "systemctl", args...).Output()
	if e != nil {
		return nil, fmt.Errorf("query managed systemd unit: %w", e)
	}
	return b, nil
}
func RestoreCondition(name string) string {
	marker := restorestate.Incomplete
	if name == SingboxUnit {
		marker = restorestate.Runtime
	}
	result := "# razdacha: managed restore start inhibitor\n[Unit]\nConditionPathExists=!/var/lib/razdacha/" + marker + "\n"
	if name == SingboxUnit {
		result += "[Service]\nExecCondition=/usr/local/bin/razdacha runtime-start-allowed\n"
	}
	return result
}
func (s RestoreServices) Show(ctx context.Context, unit string) (map[string]string, error) {
	if s.Query == nil {
		return nil, errors.New("missing managed service query adapter")
	}
	props := []string{"FragmentPath", "DropInPaths", "ExecStart", "ExecStartPre", "ExecStartPost", "ExecCondition", "Environment", "EnvironmentFiles", "ActiveState", "SubState", "UnitFileState", "MainPID", "ControlPID", "ControlGroup"}
	args := []string{"show", unit, "--no-pager"}
	for _, p := range props {
		args = append(args, "--property="+p)
	}
	b, e := s.Query(ctx, args...)
	if e != nil {
		return nil, e
	}
	out := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("malformed managed unit properties")
		}
		if _, exists := out[k]; exists {
			return nil, errors.New("duplicate managed unit property")
		}
		out[k] = v
	}
	for _, p := range props {
		if _, ok := out[p]; !ok {
			return nil, fmt.Errorf("missing managed unit property %s", p)
		}
	}
	return out, nil
}

var managedExec = regexp.MustCompile(`^\{ path=([^ ;]+) ; argv\[\]=([^;]+) ;[^{}]*\}$`)

func (s RestoreServices) Preflight(ctx context.Context) (RestoreServiceState, error) {
	if _, e := readManaged(filepath.Join(s.Root, "/sys/fs/cgroup/cgroup.controllers")); e != nil {
		return nil, fmt.Errorf("restore/recovery requires readable unified cgroup-v2 evidence: %w", e)
	}
	result := make(RestoreServiceState)
	for _, unit := range []string{DaemonUnit, SingboxUnit} {
		p, e := s.Show(ctx, unit)
		if e != nil {
			return nil, e
		}
		fragment := filepath.Join(DefaultUnitDir, unit)
		if p["FragmentPath"] != fragment {
			return nil, errors.New("restore requires canonical managed unit fragments")
		}
		var expected string
		if unit == DaemonUnit {
			expected, e = RenderDaemonUnit("/usr/local/bin/razdachad")
		} else {
			expected, e = RenderSingboxUnit("/usr/local/bin/sing-box", "/etc/sing-box/config.json")
		}
		if e != nil {
			return nil, e
		}
		b, e := readManaged(filepath.Join(s.Root, fragment))
		if e != nil {
			return nil, e
		}
		if string(b) != expected {
			return nil, errors.New("restore refuses custom managed unit content")
		}
		drop := filepath.Join(DefaultUnitDir, unit+".d", RestoreDropIn)
		if p["DropInPaths"] != drop {
			return nil, errors.New("restore refuses unknown unit drop-ins")
		}
		if p["DropInPaths"] != "" {
			b, e = readManaged(filepath.Join(s.Root, drop))
			if e != nil || string(b) != RestoreCondition(unit) {
				return nil, errors.New("invalid restore unit inhibitor")
			}
		}
		for _, key := range []string{"ExecStartPre", "ExecStartPost", "Environment", "EnvironmentFiles"} {
			if p[key] != "" {
				return nil, errors.New("restore refuses additional managed unit execution/environment")
			}
		}
		if unit == DaemonUnit {
			if p["ExecCondition"] != "" {
				return nil, errors.New("unexpected daemon ExecCondition")
			}
		} else {
			m := managedExec.FindStringSubmatch(p["ExecCondition"])
			if len(m) != 3 || m[1] != "/usr/local/bin/razdacha" || strings.TrimSpace(m[2]) != "/usr/local/bin/razdacha runtime-start-allowed" {
				return nil, errors.New("missing independent runtime restore start enforcement")
			}
		}
		argv := "/usr/local/bin/razdachad"
		binary := argv
		if unit == SingboxUnit {
			binary = "/usr/local/bin/sing-box"
			argv = binary + " -D /var/lib/sing-box run -c /etc/sing-box/config.json"
		}
		m := managedExec.FindStringSubmatch(p["ExecStart"])
		if len(m) != 3 || m[1] != binary || strings.TrimSpace(m[2]) != argv {
			return nil, errors.New("restore refuses unsupported effective ExecStart")
		}
		switch p["ActiveState"] {
		case "active", "inactive", "failed":
		default:
			return nil, errors.New("restore refuses transitional or unknown service state")
		}
		switch p["UnitFileState"] {
		case "enabled", "disabled":
		default:
			return nil, errors.New("restore refuses unsupported service enable state")
		}
		if cg := p["ControlGroup"]; cg != "" && cg != "/system.slice/"+unit {
			return nil, errors.New("restore refuses unsupported managed service cgroup")
		}
		result[unit] = RestoreUnitState{Active: p["ActiveState"], Enabled: p["UnitFileState"]}
	}
	// The config path must not resolve through a symlink, nor share a loaded directory.
	path := filepath.Join(s.Root, "/etc/sing-box/config.json")
	for _, p := range []string{filepath.Dir(path), path} {
		if i, e := os.Lstat(p); e == nil {
			if i.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("restore refuses symlink runtime config paths")
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	return result, nil
}
func readManaged(path string) ([]byte, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	stat, ok := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || i.Mode().Perm()&0o022 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("managed service file is not owner controlled")
	}
	return os.ReadFile(path)
}

func (s RestoreServices) Inhibit(ctx context.Context, dir string) error {
	if s.Run == nil {
		return errors.New("missing managed service runner")
	}
	for _, unit := range []string{DaemonUnit, SingboxUnit} {
		marker := restorestate.Incomplete
		if unit == SingboxUnit {
			marker = restorestate.Runtime
		}
		if e := restorestate.WritePrivate(filepath.Join(dir, marker), []byte("1\n")); e != nil {
			return e
		}
	}
	// Install both durable restrictions before any reload can fail halfway.
	for _, unit := range []string{DaemonUnit, SingboxUnit} {
		parent := filepath.Join(s.Root, DefaultUnitDir, unit+".d")
		if e := os.MkdirAll(parent, 0o755); e != nil {
			return e
		}
		if e := restorestate.SyncDir(filepath.Dir(parent)); e != nil {
			return e
		}
		if e := restorestate.WritePrivate(filepath.Join(parent, RestoreDropIn), []byte(RestoreCondition(unit))); e != nil {
			return e
		}
		if e := s.Run(ctx, "daemon-reload"); e != nil {
			return e
		}
		p, e := s.Show(ctx, unit)
		if e != nil {
			return e
		}
		if p["DropInPaths"] != filepath.Join(DefaultUnitDir, unit+".d", RestoreDropIn) {
			return errors.New("managed restore inhibitor not loaded")
		}
	}
	return nil
}
func (s RestoreServices) Stop(ctx context.Context) error {
	return s.stopUnits(ctx, []string{DaemonUnit, SingboxUnit})
}
func (s RestoreServices) stopUnits(ctx context.Context, units []string) error {
	for _, unit := range units {
		if e := s.Run(ctx, "stop", unit); e != nil {
			return e
		}
		p, e := s.Show(ctx, unit)
		if e != nil {
			return e
		}
		if (p["ActiveState"] != "inactive" && p["ActiveState"] != "failed") || p["MainPID"] != "0" || p["ControlPID"] != "0" {
			return errors.New("managed service did not quiesce")
		}
		// systemd's default KillMode=control-group drains all descendants before stop returns.
		// Reject leftover cgroup processes, including a child whose MainPID already exited.
		if cg := p["ControlGroup"]; cg != "" {
			if cg != "/system.slice/"+unit {
				return errors.New("unsupported managed unit cgroup")
			}
			group := filepath.Join(s.Root, "/sys/fs/cgroup", cg)
			b, e := readManaged(filepath.Join(group, "cgroup.procs"))
			if errors.Is(e, os.ErrNotExist) {
				if _, gone := os.Lstat(group); !errors.Is(gone, os.ErrNotExist) {
					return errors.New("managed cgroup exists without readable process evidence")
				}
			} else if e != nil {
				return e
			}
			if len(strings.TrimSpace(string(b))) != 0 {
				return errors.New("managed unit retains running processes")
			}
		}
	}
	return nil
}

// AttestRestoreBinary is metadata-only: older binaries reject the unknown flag.
func AttestRestoreBinary(ctx context.Context, path string) ([]byte, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	stat, ok := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || i.Mode().Perm()&0o022 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("restore executable is not a trusted regular file")
	}
	digest, e := binaryIdentity(path)
	if e != nil {
		return nil, e
	}
	out, e := exec.CommandContext(ctx, path, "-restore-capabilities").Output()
	if e != nil {
		return nil, errors.New("installed executable lacks safe restore capability; install compatible binaries first")
	}
	if strings.TrimSpace(string(out)) != fmt.Sprintf(`{"restore_protocol":%d}`, restorestate.Protocol) {
		return nil, errors.New("incompatible or ambiguous safe restore capability")
	}
	again, e := binaryIdentity(path)
	if e != nil || string(digest) != string(again) {
		return nil, errors.New("restore executable changed during attestation")
	}
	return digest, nil
}

// StopRuntime is used by publication Abort; it must not stop the authorizing daemon.
func (s RestoreServices) StopRuntime(ctx context.Context) error {
	return s.stopUnits(ctx, []string{SingboxUnit})
}

func (s RestoreServices) RuntimeReady(ctx context.Context) error {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		p, e := s.Show(ctx, SingboxUnit)
		if e == nil && p["ActiveState"] == "active" && p["MainPID"] != "0" {
			request, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:9090/version", nil)
			if e != nil {
				return e
			}
			response, e := client.Do(request)
			if e == nil {
				var body struct {
					Version string `json:"version"`
				}
				decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&body)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK && decodeErr == nil && body.Version != "" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("checked restored runtime did not become ready")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (u *UnitInstaller) ensureRestoreCondition(unit string) (bool, error) {
	dir := filepath.Join(u.Root, u.dir(), unit+".d")
	path := filepath.Join(dir, RestoreDropIn)
	expected := []byte(RestoreCondition(unit))
	b, e := os.ReadFile(path)
	if e == nil {
		if string(b) != string(expected) {
			return false, errors.New("managed restore condition is foreign or modified")
		}
		return false, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return false, e
	}
	if e = os.MkdirAll(dir, 0o755); e != nil {
		return false, e
	}
	if e = restorestate.SyncDir(filepath.Dir(dir)); e != nil {
		return false, e
	}
	return true, restorestate.WritePrivate(path, expected)
}

func binaryIdentity(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	i, e := f.Stat()
	if e != nil {
		return nil, e
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return nil, e
	}
	stat, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("missing executable identity")
	}
	return []byte(fmt.Sprintf("%x:%d:%d", h.Sum(nil), stat.Dev, stat.Ino)), nil
}
