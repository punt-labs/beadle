package identity

import (
	"encoding/json"
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
	GPGKeyID   string `yaml:"gpg_key_id"`
	OwnerEmail string `yaml:"owner_email"`
}

// legacyConfig is the subset of email.json needed for legacy fallback.
type legacyConfig struct {
	FromAddress string `json:"from_address"`
}

// Resolver resolves the active identity from ethos or beadle fallbacks.
// It reads files directly (sidecar pattern, no subprocess).
type Resolver struct {
	ethosDir  string // ~/.punt-labs/ethos
	beadleDir string // ~/.punt-labs/beadle
	repoDir   string // cwd (for repo-local .punt-labs/ethos/config.yaml)
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
//  6. Beadle email.json → from_address (legacy)
// validateHandle rejects handles containing path separators or parent
// directory references to prevent path traversal attacks.
func validateHandle(handle string) error {
	if strings.ContainsAny(handle, "/\\") {
		return fmt.Errorf("ethos handle %q contains path separator", handle)
	}
	if handle == ".." || strings.HasPrefix(handle, "..") {
		return fmt.Errorf("ethos handle %q contains parent directory reference", handle)
	}
	return nil
}

func (r *Resolver) Resolve() (*Identity, error) {
	// Try ethos-based resolution (steps 1-4)
	handle := r.resolveHandle()
	if handle != "" {
		if err := validateHandle(handle); err != nil {
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

	// Step 6: legacy email.json
	id, err = r.fromLegacy()
	if err == nil {
		return id, nil
	}

	return nil, fmt.Errorf("no identity found: checked ethos (%s), default-identity, email.json in %s", r.ethosDir, r.beadleDir)
}

// resolveHandle returns the active ethos handle, or "" if unavailable.
// Checks repo-local config first, then global active file.
func (r *Resolver) resolveHandle() string {
	// Step 1: repo-local ethos config
	if r.repoDir != "" {
		repoConfig := filepath.Join(r.repoDir, ".punt-labs", "ethos", "config.yaml")
		handle, err := readRepoEthosConfig(repoConfig)
		if err == nil && handle != "" {
			return handle
		}
	}

	// Step 2: global ethos active file
	activePath := filepath.Join(r.ethosDir, "active")
	data, err := os.ReadFile(activePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// repoEthosConfig is the structure of .punt-labs/ethos/config.yaml.
type repoEthosConfig struct {
	Active string `yaml:"active"`
}

// readRepoEthosConfig reads the active handle from a repo-local config.
func readRepoEthosConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg repoEthosConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg.Active, nil
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
		id.OwnerEmail = ext.OwnerEmail
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read beadle extension %s: %w", extPath, err)
	}

	// Default: if no owner set, the identity is its own owner (human identity)
	if id.OwnerEmail == "" {
		id.OwnerEmail = id.Email
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
	return &Identity{
		Email:      email,
		OwnerEmail: email, // default identity is its own owner
		Source:     "default",
	}, nil
}

// fromLegacy reads from_address from the legacy email.json.
func (r *Resolver) fromLegacy() (*Identity, error) {
	path := filepath.Join(r.beadleDir, "email.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg legacyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.FromAddress == "" {
		return nil, fmt.Errorf("email.json has no from_address")
	}
	return &Identity{
		Email:      cfg.FromAddress,
		OwnerEmail: cfg.FromAddress, // legacy identity is its own owner
		Source:     "legacy",
	}, nil
}
