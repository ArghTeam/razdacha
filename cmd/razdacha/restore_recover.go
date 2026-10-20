package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func runRestoreRecovery(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("restore-recover", flag.ContinueOnError)
	operation := flags.String("operation", "", "private current-destination recovery directory; required if trusted state is corrupt/missing")
	activate := flags.Bool("activate-destination", false, "acknowledge reusing original destination credentials and callbacks after safe rollback; preserve any prior callback hold")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected recovery arguments")
	}
	return recoverRestore(ctx, restoreOptions{dbPath: defaultDBPath, services: &packaging.RestoreServices{Query: packaging.SystemdQuery, Run: systemctl}, attest: packaging.AttestRestoreBinary, closeIngress: installClientGuard}, *operation, *activate)
}

func recoverRestore(ctx context.Context, opts restoreOptions, operation string, activate bool) error {
	dbPath, e := filepath.Abs(filepath.Join(opts.root, opts.dbPath))
	if e != nil {
		return e
	}
	dir := filepath.Dir(dbPath)
	lock, e := restorestate.Acquire(dir, -1)
	if e != nil {
		return e
	}
	defer func() { _ = lock.Close() }()
	current, stateErr := restorestate.RecoveryState(dir)
	if operation == "" {
		if stateErr != nil {
			return fmt.Errorf("trusted state cannot identify recovery; provide -operation: %w", stateErr)
		}
		operation = current.Recovery
	}
	operation, e = filepath.Abs(operation)
	if e != nil {
		return e
	}
	if filepath.Dir(operation) != dir || !strings.HasPrefix(filepath.Base(operation), ".restore-") {
		return errors.New("recovery directory must be a private restore operation directly inside this installation")
	}
	info, e := os.Lstat(operation)
	if e != nil {
		return e
	}
	st, owned := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || !owned || st.Uid != uint32(os.Geteuid()) {
		return errors.New("recovery operation must be a private nonsymlink directory")
	}
	recovery, e := restorestate.ReadRecoveryMetadata(operation)
	if e != nil {
		return e
	}
	if e = validateRecoveryTargets(recovery, dir, dbPath, opts.services); e != nil {
		return e
	}
	if opts.services == nil || opts.attest == nil || opts.closeIngress == nil {
		return errors.New("recovery requires contained service/capability/ingress adapters")
	}
	if _, e = opts.services.Preflight(ctx); e != nil {
		return e
	}
	identities := make(map[string][]byte)
	for _, binary := range []string{filepath.Join(opts.services.Root, defaultDaemon), filepath.Join(opts.services.Root, defaultBinDir, "razdacha")} {
		identities[binary], e = opts.attest(ctx, binary)
		if e != nil {
			return e
		}
	}
	var originalServices packaging.RestoreServiceState
	if e = json.Unmarshal(recovery.Services, &originalServices); e != nil {
		return e
	}
	if activate {
		retry := stateErr == nil && current.Phase == "complete" && current.RecoveryActivated && current.RuntimeInhibited
		if stateErr != nil || (current.Phase != "recovered" && !retry) || current.Recovery != operation || !recovery.Complete {
			return errors.New("first perform safe restore-recover; activation requires its complete recovered state")
		}
		if !recovery.DatabasePresent {
			return errors.New("original destination had no database; rollback is safely inhibited and requires explicit fresh-install recovery")
		}
		if e = verifyRecoveredDatabase(dbPath, recovery); e != nil {
			return e
		}
		priorHeld, e := restorestate.PreviousHold(dir, operation, recovery)
		if e != nil {
			return e
		}
		for binary, identity := range identities {
			again, e := opts.attest(ctx, binary)
			if e != nil {
				return e
			}
			if !bytes.Equal(identity, again) {
				return errors.New("managed executable changed during recovery activation")
			}
		}
		// Callback consent and original WG enable intent are separate from import activation.
		// Runtime stays inhibited until another explicit guarded Apply of recovered policy.
		current.Phase = "complete"
		current.Held = priorHeld
		current.RuntimeInhibited = true
		current.RecoveryActivated = true
		if e = restorestate.Remove(filepath.Join(dir, restorestate.Incomplete)); e != nil {
			return e
		}
		if e = restorestate.Save(dir, *current); e != nil {
			return e
		}
		for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
			action := originalServices[unit].Enabled
			if action == "enabled" {
				action = "enable"
			} else {
				action = "disable"
			}
			if e = opts.services.Run(ctx, action, unit); e != nil {
				return e
			}
		}
		if originalServices[packaging.DaemonUnit].Active == "active" {
			if e = opts.services.Run(ctx, "start", packaging.DaemonUnit); e != nil {
				return e
			}
		}
		fmt.Println("Original destination recovery activated. Prior callback hold is preserved; runtime and client ingress require explicit Apply. Imported WireGuard flags are not enabled.")
		return nil
	}
	record := restorestate.State{Version: restorestate.Protocol, Operation: filepath.Base(operation), Recovery: operation, Phase: "recovering", Held: true, RuntimeInhibited: true}
	if e = restorestate.Save(dir, record); e != nil {
		return e
	}
	if e = opts.services.Inhibit(ctx, dir); e != nil {
		return e
	}
	if e = opts.services.Stop(ctx); e != nil {
		return e
	}
	if e = opts.closeIngress(); e != nil {
		return e
	}
	if e = restorestate.Remove(filepath.Join(dir, restorestate.PermitFile)); e != nil {
		return e
	}
	if !recovery.Complete {
		// Complete=false durably proves that imported DB/runtime/authority were never
		// mutated. Capture that still-current destination, then recover the complete set.
		recovery, e = collectDestinationRecovery(ctx, operation, dbPath, opts.services.Root, recovery)
		if e != nil {
			return e
		}
		if e = validateRecoveryTargets(recovery, dir, dbPath, opts.services); e != nil {
			return e
		}
	}
	if e = quarantineExtraNative(ctx, operation, dir, recovery); e != nil {
		return e
	}
	for _, file := range recovery.Files {
		if e = ctx.Err(); e != nil {
			return e
		}
		if isRecoverySafetyTarget(file.Target, dir, opts.services.Root) {
			continue
		}
		if file.Target == dbPath {
			b, e := restorestate.ReadPrivate(filepath.Join(operation, file.Name), 256<<20)
			if e != nil {
				return e
			}
			staged, e := os.CreateTemp(operation, "rollback-*.db")
			if e != nil {
				return e
			}
			path := staged.Name()
			if e = staged.Close(); e != nil {
				return e
			}
			if e = restorestate.WritePrivate(path, b); e != nil {
				return e
			}
			if e = store.ValidateRestoreSchema(ctx, path); e != nil {
				return e
			}
			if e = replaceDBUsing(path, dbPath, opts.dbFiles); e != nil {
				return e
			}
			continue
		}
		if file.Missing {
			if e = restorestate.Remove(file.Target); e != nil {
				return e
			}
			continue
		}
		anchor := dir
		if file.Target == filepath.Join(opts.services.Root, singbox.DefaultConfigPath) {
			anchor = filepath.Join(opts.services.Root, "/etc")
		}
		if e = ensureRecoveryParent(file.Target, anchor); e != nil {
			return e
		}
		b, e := restorestate.ReadPrivate(filepath.Join(operation, file.Name), 256<<20)
		if e != nil {
			return e
		}
		if e = restorestate.WritePrivate(file.Target, b); e != nil {
			return e
		}
	}
	if !recovery.DatabasePresent {
		if _, e = os.Stat(dbPath); e == nil {
			if e = store.CheckpointAt(ctx, dbPath); e != nil {
				return e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		for _, suffix := range []string{"-wal", "-shm", ""} {
			if e = restorestate.Remove(dbPath + suffix); e != nil {
				return e
			}
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	record.Phase = "recovered"
	if e = restorestate.Save(dir, record); e != nil {
		return e
	}
	fmt.Println("Current destination snapshot restored coherently. Both services remain inhibited and client ingress guarded. Review original destination credentials/settings, then use restore-recover -activate-destination before explicit Apply. The migration archive is not rollback authority.")
	return nil
}

func validateRecoveryTargets(r restorestate.Recovery, dir, db string, services *packaging.RestoreServices) error {
	if services == nil {
		return errors.New("missing recovery service topology")
	}
	allowed := map[string]bool{db: true, filepath.Join(services.Root, singbox.DefaultConfigPath): true, filepath.Join(dir, networkstate.CommittedFile): true, filepath.Join(dir, networkstate.BootstrapFile): true}
	native := filepath.Join(dir, "lists", "native") + string(os.PathSeparator)
	required := map[string]bool{}
	for path := range allowed {
		if path != db {
			required[path] = false
		}
	}
	for _, name := range []string{restorestate.File, restorestate.Incomplete, restorestate.Runtime, restorestate.PermitFile} {
		required[filepath.Join(dir, name)] = false
	}
	for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
		required[filepath.Join(services.Root, packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn)] = false
	}
	databases := 0
	for _, f := range r.Files {
		if _, needed := required[f.Target]; needed {
			required[f.Target] = true
		}
		if f.Target == db {
			databases++
		}
		if !allowed[f.Target] && !isRecoverySafetyTarget(f.Target, dir, services.Root) && !strings.HasPrefix(f.Target, native) {
			return errors.New("recovery manifest targets an unsupported destination")
		}
	}
	if r.Complete {
		for _, present := range required {
			if !present {
				return errors.New("complete recovery omits required destination metadata")
			}
		}
	}
	if r.Complete && (r.DatabasePresent && databases != 1 || !r.DatabasePresent && databases != 0) {
		return errors.New("recovery database presence is inconsistent")
	}
	return nil
}
func isRecoverySafetyTarget(path, dir, root string) bool {
	for _, name := range []string{restorestate.File, restorestate.Incomplete, restorestate.Runtime, restorestate.PermitFile} {
		if path == filepath.Join(dir, name) {
			return true
		}
	}
	for _, unit := range []string{packaging.DaemonUnit, packaging.SingboxUnit} {
		if path == filepath.Join(root, packaging.DefaultUnitDir, unit+".d", packaging.RestoreDropIn) {
			return true
		}
	}
	return false
}

// Inspect each component before creation; never follow a newly introduced symlink.
func ensureRecoveryParent(path, anchor string) error {
	parent := filepath.Dir(path)
	relative, e := filepath.Rel(anchor, parent)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("recovery parent escapes its fixed root")
	}
	current := anchor
	components := []string{}
	if relative != "." {
		components = strings.Split(relative, string(os.PathSeparator))
	}
	for index := -1; index < len(components); index++ {
		if index >= 0 {
			current = filepath.Join(current, components[index])
		}
		info, e := os.Lstat(current)
		if errors.Is(e, os.ErrNotExist) && index >= 0 {
			if e = os.Mkdir(current, 0o700); e != nil {
				return e
			}
			if e = restorestate.SyncDir(filepath.Dir(current)); e != nil {
				return e
			}
			info, e = os.Lstat(current)
		}
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return errors.New("recovery parent resolves through a symlink")
		}
	}
	return nil
}
func verifyRecoveredDatabase(db string, r restorestate.Recovery) error {
	for _, file := range r.Files {
		if file.Target == db && !file.Missing {
			b, e := restorestate.ReadPrivate(db, 256<<20)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != file.Hash {
				return errors.New("recovered destination DB changed before activation; repeat safe recovery")
			}
			return nil
		}
	}
	return errors.New("recovery set lacks the original destination database")
}

// Preserve newly acquired artifacts separately, so rollback restores exactly the
// original dependency set rather than retaining imported-generation files.
func quarantineExtraNative(ctx context.Context, operation, dir string, recovery restorestate.Recovery) error {
	expected := make(map[string]bool)
	for _, file := range recovery.Files {
		expected[file.Target] = true
	}
	root := filepath.Join(dir, "lists", "native")
	if e := ensureRecoveryParent(filepath.Join(root, ".recovery-parent-check"), dir); e != nil {
		return e
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
		if errors.Is(e, os.ErrNotExist) && path == root {
			return nil
		}
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		if expected[path] {
			return nil
		}
		if e = ensureRecoveryParent(path, dir); e != nil {
			return e
		}
		if _, e = restorestate.CaptureFile(operation, path); e != nil {
			return e
		}
		return restorestate.Remove(path)
	})
}
