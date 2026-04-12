package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ethosIdentity is the subset of ethos identity YAML that beadle reads.
type ethosIdentity struct {
	Handle string `yaml:"handle"`
	Name   string `yaml:"name"`
	Email  string `yaml:"email"`
}

// beadleExtension is the beadle namespace in ethos extensions.
type beadleExtension struct {
	GPGKeyID string `yaml:"gpg_key_id"`
}

// Resolver resolves the active identity from ethos or beadle fallbacks.
// It reads files directly (sidecar pattern, no subprocess).
type Resolver struct {
	ethosDir  string // ~/.punt-labs/ethos
	beadleDir string // ~/.punt-labs/beadle
	repoDir   string // cwd (for repo-local .punt-labs/ethos.yaml)
}

// NewResolver creates a Resolver with the given directory roots.
func NewResolver(ethosDir, beadleDir, repoDir string) *Resolver {
	return &Resolver{
		ethosDir:  ethosDir,
		beadleDir: beadleDir,
		repoDir:   repoDir,
	}
}

// Resolve returns the active identity by walking the resolution chain:
//  1. Repo-local ethos config → handle
//  2. Global ethos active file → handle
//  3. Handle → ethos identity YAML → email, name
//  4. Handle → beadle extension → gpg_key_id (optional)
//  5. Beadle default-identity file → email (no handle)
//
// ValidateHandle rejects handles containing path separators or parent
// directory references to prevent path traversal attacks.
func ValidateHandle(handle string) error {
	if strings.ContainsAny(handle, "/\\") {
		return fmt.Errorf("ethos handle %q contains path separator", handle)
	}
	if handle == ".." || strings.HasPrefix(handle, "..") {
		return fmt.Errorf("ethos handle %q contains parent directory reference", handle)
	}
	return nil
}

// ValidateEmailAsPath rejects email strings that would cause path traversal
// when used as directory names. Called before using email in filepath.Join.
func ValidateEmailAsPath(email string) error {
	if email == "" {
		return fmt.Errorf("email is empty")
	}
	if strings.ContainsAny(email, "/\\") {
		return fmt.Errorf("email %q contains path separator", email)
	}
	if email == ".." || strings.Contains(email, "..") {
		return fmt.Errorf("email %q contains parent directory reference", email)
	}
	return nil
}

func (r *Resolver) Resolve() (*Identity, error) {
	// Try ethos-based resolution (steps 1-4)
	handle, err := r.resolveHandle()
	if err != nil {
		return nil, err
	}
	if handle != "" {
		if err := ValidateHandle(handle); err != nil {
			return nil, err
		}
		id, err := r.fromEthos(handle)
		if err != nil {
			// Ethos has an active handle but the identity is unreadable.
			// This is an error, not a fallback — operating as the wrong
			// identity is worse than failing.
			return nil, fmt.Errorf("ethos active identity %q: %w", handle, err)
		}
		return id, nil
	}

	// Step 5: default-identity file
	id, err := r.fromDefault()
	if err == nil {
		return id, nil
	}

	return nil, fmt.Errorf("no identity found: checked ethos (%s), default-identity in %s", r.ethosDir, r.beadleDir)
}

// ResolveHandle resolves a specific ethos handle to an Identity.
// Used by switch_identity to validate and load a named identity.
func (r *Resolver) ResolveHandle(handle string) (*Identity, error) {
	if err := ValidateHandle(handle); err != nil {
		return nil, err
	}
	return r.fromEthos(handle)
}

// resolveHandle returns the active ethos handle, or "" if unavailable.
// Checks repo-local config first, then global active file.
// Returns an error if a config file exists but is corrupt (fail closed).
func (r *Resolver) resolveHandle() (string, error) {
	// Step 1: repo-local ethos config
	if r.repoDir != "" {
		repoConfig := filepath.Join(r.repoDir, ".punt-labs", "ethos.yaml")
		handle, err := readRepoEthosConfig(repoConfig)
		if err == nil && handle != "" {
			return handle, nil
		}
		// Fail closed: if file exists but is corrupt, don't fall back
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("repo-local ethos config %s: %w", repoConfig, err)
		}
	}

	// Step 2: global ethos active file
	activePath := filepath.Join(r.ethosDir, "active")
	data, err := os.ReadFile(activePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil // no ethos installed
		}
		return "", fmt.Errorf("read ethos active %s: %w", activePath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// repoEthosConfig is the structure of .punt-labs/ethos.yaml.
// The "agent" field identifies the default agent identity for the repo.
// Ethos uses "agent" (not "active") because both a human and an agent
// are active in every Claude Code session.
type repoEthosConfig struct {
	Agent  string `yaml:"agent"`
	Active string `yaml:"active"` // detect stale config using the old field name
}

// readRepoEthosConfig reads the agent handle from a repo-local config.
func readRepoEthosConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg repoEthosConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Agent == "" && cfg.Active != "" {
		return "", fmt.Errorf("parse %s: uses deprecated 'active' field — rename to 'agent'", path)
	}
	return cfg.Agent, nil
}

// fromEthos builds an Identity from ethos identity + extension files.
func (r *Resolver) fromEthos(handle string) (*Identity, error) {
	// Step 3: read identity YAML
	idPath := filepath.Join(r.ethosDir, "identities", handle+".yaml")
	data, err := os.ReadFile(idPath)
	if err != nil {
		return nil, fmt.Errorf("read ethos identity %s: %w", idPath, err)
	}

	var eid ethosIdentity
	if err := yaml.Unmarshal(data, &eid); err != nil {
		return nil, fmt.Errorf("parse ethos identity %s: %w", idPath, err)
	}
	if eid.Email == "" {
		return nil, fmt.Errorf("ethos identity %s has no email field", idPath)
	}
	if err := ValidateEmailAsPath(eid.Email); err != nil {
		return nil, fmt.Errorf("ethos identity %s: %w", idPath, err)
	}

	id := &Identity{
		Handle: handle,
		Name:   eid.Name,
		Email:  eid.Email,
		Source: "ethos",
	}

	// Step 4: read beadle extension (optional — missing file is OK, corrupt is not)
	extPath := filepath.Join(r.ethosDir, "identities", handle+".ext", "beadle.yaml")
	extData, err := os.ReadFile(extPath)
	if err == nil {
		var ext beadleExtension
		if parseErr := yaml.Unmarshal(extData, &ext); parseErr != nil {
			return nil, fmt.Errorf("parse beadle extension %s: %w", extPath, parseErr)
		}
		id.GPGKeyID = ext.GPGKeyID
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read beadle extension %s: %w", extPath, err)
	}

	return id, nil
}

// fromDefault reads the default-identity file (plain email string).
func (r *Resolver) fromDefault() (*Identity, error) {
	path := filepath.Join(r.beadleDir, "default-identity")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	email := strings.TrimSpace(string(data))
	if email == "" {
		return nil, fmt.Errorf("default-identity file is empty")
	}
	if err := ValidateEmailAsPath(email); err != nil {
		return nil, fmt.Errorf("default-identity: %w", err)
	}
	return &Identity{
		Email:  email,
		Source: "default",
	}, nil
}
