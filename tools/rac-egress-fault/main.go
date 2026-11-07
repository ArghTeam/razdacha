package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "RAC fault fixture:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("choose prepare, hold, status, cleanup, watchdog or selftest")
	}
	mode := args[0]
	fs := flag.NewFlagSet("rac-egress-fault", flag.ContinueOnError)
	id := fs.String("run-id", "", "fresh lowercase 128-bit hexadecimal run identity")
	port := fs.Int("port", 0, "fixed DO TCP port: 443 or 2443 (prepare only)")
	deadline := fs.String("deadline", "", "absolute UTC RFC3339 deadline (prepare only)")
	parentNS := fs.String("parent-netns", "", "parent namespace identity (selftest-child only)")
	enabled := fs.Bool("enable-rac-exit-fault", false, "explicitly authorize this RAC-only exit fixture")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*enabled || fs.NArg() != 0 {
		return errors.New("explicit RAC fault opt-in and named flags only are required")
	}
	if err := validRun(*id); err != nil {
		return err
	}
	if mode != "prepare" && (*port != 0 || *deadline != "") {
		return errors.New("port/deadline belong to prepare only")
	}
	if mode == "selftest-child" {
		if *port != 0 || *deadline != "" || *parentNS == "" {
			return errors.New("selftest child requires only its parent namespace identity")
		}
		return kernelSelfTestChild(*id, *parentNS)
	}
	if *parentNS != "" {
		return errors.New("parent namespace belongs to selftest-child only")
	}
	if requiresSandboxAddress(mode) {
		if err := checkHost(); err != nil {
			return err
		}
	} else if err := checkCleanupEligibility(); err != nil {
		return err
	}
	if mode == "selftest" {
		return kernelSelfTest(*id)
	}
	j := journals{root: journalRoot}
	if mode == "prepare" {
		at, err := time.Parse(time.RFC3339Nano, *deadline)
		if err != nil {
			return errors.New("deadline must be UTC RFC3339")
		}
		if at.Location() != time.UTC {
			return errors.New("deadline must use UTC Z")
		}
		now, mono, err := clockNow()
		if err != nil {
			return err
		}
		ttl := at.Sub(now)
		if ttl < 5*time.Second || ttl > maxLifetime {
			return errors.New("prepare deadline must be 5 to 90 seconds away")
		}
		boot, err := bootID()
		if err != nil {
			return err
		}
		ns, err := networkNamespace()
		if err != nil {
			return err
		}
		p := plan{Version: 1, RunID: *id, Port: *port, PreparedAt: now.UTC(), Deadline: at.UTC(), DeadlineMonoUS: mono + uint64(ttl/time.Microsecond), BootID: boot, NetworkNS: ns, Phase: "prepared"}
		if err = j.prepare(p); err != nil {
			return err
		}
		return emitState(j, p)
	}
	if mode != "hold" && mode != "status" && mode != "cleanup" && mode != "watchdog" {
		return errors.New("unknown fixture command")
	}
	var p plan
	if err := j.withLock(*id, func() error {
		var err error
		p, err = j.load(*id)
		if err != nil {
			return err
		}
		return sameEnvironment(p)
	}); err != nil {
		return err
	}
	switch mode {
	case "status":
		return emitState(j, p)
	case "cleanup":
		return cleanup(j, *id)
	case "watchdog":
		return watchdog(j, p)
	case "hold":
		return hold(j, p)
	}
	return nil
}

func sameEnvironment(p plan) error {
	current, err := bootID()
	if err != nil {
		return err
	}
	namespace, err := networkNamespace()
	if err != nil {
		return err
	}
	return validateRunEnvironment(p, current, namespace)
}

func emitState(j journals, p plan) error {
	return j.withLock(p.RunID, func() error {
		current, err := j.load(p.RunID)
		if err != nil {
			return err
		}
		if err = sameEnvironment(current); err != nil {
			return err
		}
		fw, err := newFirewall()
		if err != nil {
			return err
		}
		defer fw.close()
		c, present, err := fw.inspect(current)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(observe(current, j.path(p.RunID), c, present))
	})
}

func cleanup(j journals, id string) error {
	err := j.withLock(id, func() error {
		p, err := j.load(id)
		if err != nil {
			return err
		}
		if err = sameEnvironment(p); err != nil {
			return err
		}
		fw, err := newFirewall()
		if err != nil {
			return err
		}
		defer fw.close()
		return revoke(&p, j.save, fw)
	})
	if err != nil {
		return err
	}
	var p plan
	if err = j.withLock(id, func() error { var e error; p, e = j.load(id); return e }); err != nil {
		return err
	}
	return emitState(j, p)
}

func hold(j journals, p plan) error {
	if err := verifyArmedUnits(p); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	err := j.withLock(p.RunID, func() error {
		current, e := j.load(p.RunID)
		if e != nil {
			return e
		}
		if e = sameEnvironment(current); e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fw, e := newFirewall()
		if e != nil {
			return e
		}
		defer fw.close()
		var clockErr error
		now := func() (time.Time, uint64) {
			w, m, e := clockNow()
			if ctx.Err() != nil {
				return current.Deadline, current.DeadlineMonoUS
			}
			if e != nil {
				clockErr = e
				return current.Deadline, current.DeadlineMonoUS
			}
			return w, m
		}
		if e = install(&current, now, j.save, fw); e != nil {
			return errors.Join(e, clockErr)
		}
		p = current
		return nil
	})
	if err != nil {
		return err
	}
	// Always revoke before returning, including stdout or clock failures.
	defer func() { stop() }()
	if err = emitState(j, p); err != nil {
		return errors.Join(err, cleanup(j, p.RunID))
	}
	_, mono, err := clockNow()
	if err != nil {
		return errors.Join(err, cleanup(j, p.RunID))
	}
	remaining := time.Duration(0)
	if mono < p.DeadlineMonoUS {
		remaining = time.Duration(p.DeadlineMonoUS-mono) * time.Microsecond
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return cleanup(j, p.RunID)
}
