// Package ippolicy defines shared IPv4 local-network boundaries for kernel and proxy policy.
package ippolicy

// LocalIPv4 returns an independent list of private and special destinations. The
// kernel exempts these from TProxy; whole-Internet peers cannot forward to them.
func LocalIPv4() []string {
	return []string{"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4"}
}
