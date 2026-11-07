package main

// Fresh users need authenticated panel access before importing a VPN profile.
func effectivePanelOptions(fresh bool, opts setupOptions) setupOptions {
	if fresh && !opts.publicSet {
		opts.public, opts.publicSet = true, true
	}
	return opts
}

// Direct CLI invocation honors the same explicit panel control as install.sh.
func panelModeEnvironment(opts *setupOptions, lookup func(string) (string, bool)) {
	if opts.publicSet {
		return
	}
	raw, ok := lookup("RAZDACHA_PUBLIC")
	if !ok {
		return
	}
	switch raw {
	case "", "0", "no", "No", "NO", "false", "False", "FALSE":
		opts.public = false
	default:
		opts.public = true
	}
	opts.publicSet = true
}
