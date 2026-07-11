//go:build !pro

package buildinfo

// Pro reports whether this binary was compiled with the pro check set.
const Pro = false

// Edition is the human-readable build name.
const Edition = "Community"
