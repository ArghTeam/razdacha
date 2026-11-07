//go:build !linux

package main

import (
	"errors"
	"time"
)

func checkHost() error                      { return errors.New("this fixture requires Linux on the authorized RAC host") }
func clockNow() (time.Time, uint64, error)  { return time.Time{}, 0, checkHost() }
func bootID() (string, error)               { return "", checkHost() }
func newFirewall() (*kernelFirewall, error) { return nil, checkHost() }
func verifyArmedUnits(plan) error           { return checkHost() }
func watchdog(journals, plan) error         { return checkHost() }

type kernelFirewall struct{}

func (*kernelFirewall) close()                               {}
func (*kernelFirewall) install(plan) error                   { return checkHost() }
func (*kernelFirewall) inspect(plan) (counters, bool, error) { return counters{}, false, checkHost() }
func (*kernelFirewall) remove(plan) error                    { return checkHost() }

func kernelSelfTest(string) error { return checkHost() }

func kernelSelfTestChild(string, string) error { return checkHost() }

func checkCleanupEligibility() error { return checkHost() }

func networkNamespace() (string, error) { return "", checkHost() }
