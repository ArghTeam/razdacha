package main

import "testing"

func TestUnitCommandRequiresActualExecutableAndExactArguments(t *testing.T) {
	exe := "/opt/fixture/racfault"
	id := fixturePlan().RunID
	good := "{ path=" + exe + " ; argv[]=" + exe + " hold --run-id " + id + " --enable-rac-exit-fault ; ignore_errors=no ; }"
	if !exactExec(good, exe, "hold", id) {
		t.Fatal("expected command rejected")
	}
	for _, v := range []string{
		"{ path=/usr/bin/foreign ; argv[]=" + exe + " hold --run-id " + id + " --enable-rac-exit-fault ; }",
		"{ path=" + exe + " ; argv[]=" + exe + " cleanup --run-id " + id + " --enable-rac-exit-fault ; }",
		"{ path=" + exe + " ; argv[]=" + exe + " hold --run-id " + id + " --enable-rac-exit-fault --other ; }",
		good + good,
	} {
		if exactExec(v, exe, "hold", id) {
			t.Fatal("foreign or extended command accepted")
		}
	}
}

func TestSystemdUnitPathEscapesPunctuation(t *testing.T) {
	if unitPath("abc-def.service") != "/org/freedesktop/systemd1/unit/abc_2ddef_2eservice" {
		t.Fatal("unit path encoding differs")
	}
}

func TestKernelSelfTestCannotUseHostOrParentNamespace(t *testing.T) {
	if err := validateIsolatedNamespace("4:11", "4:11", "4:12"); err != nil {
		t.Fatal(err)
	}
	for _, v := range [][3]string{{"4:11", "4:11", "4:11"}, {"4:10", "4:11", "4:11"}, {"fake", "4:11", "4:12"}} {
		if validateIsolatedNamespace(v[0], v[1], v[2]) == nil {
			t.Fatal("host or invalid namespace accepted")
		}
	}
}

func TestWatchdogNeedsFutureRootAndHostNetworkPrivileges(t *testing.T) {
	good := map[string]string{"User": "root", "DynamicUser": "no", "PrivateNetwork": "no", "CapabilityBoundingSet": "cap_net_admin cap_net_raw"}
	if err := validateWatchdogContext(good); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"User": "nobody", "DynamicUser": "yes", "PrivateNetwork": "yes", "NetworkNamespacePath": "/run/other", "JoinsNamespaceOf": "foreign.service", "CapabilityBoundingSet": "cap_net_raw"} {
		v := map[string]string{}
		for k, x := range good {
			v[k] = x
		}
		v[key] = value
		if validateWatchdogContext(v) == nil {
			t.Fatalf("unsafe watchdog %s accepted", key)
		}
	}
}
