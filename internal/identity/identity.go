// Package identity resolves which identity beadle operates as.
// Identity is owned by ethos (sidecar pattern); beadle reads it
// via direct file access — no subprocess, no import dependency.
package identity

// Identity represents the active beadle identity.
type Identity struct {
	Handle     string // ethos handle ("claude"), empty for non-ethos sources
	Name       string // display name ("Claude Agento")
	Email      string // email address — directory key
	GPGKeyID   string // from beadle extension (optional)
	OwnerEmail string // owner's email — owner always gets rwx on contacts
	Source     string // "ethos", "default", or "legacy"
}
