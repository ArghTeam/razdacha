package ingressguard

// ReadConfig reads bounded exact bytes from the private owner-controlled runtime
// config using the same no-symlink file/parent checks as the startup helper.
// Publication callers hold WithLock while comparing and releasing kernel guards.
func ReadConfig(path string) ([]byte, error) {
	return readControlled(path, maxConfigBytes, false)
}
