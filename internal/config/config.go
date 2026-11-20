package config

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/master2sid2/sshm/internal/fsutil"
)

// AuthType defines how we authenticate to the remote host.
type AuthType string

const (
	AuthPassword AuthType = "password"
	AuthKey      AuthType = "key"
	AuthAgent    AuthType = "agent"
)

// TunnelDirection defines which end initiates the port binding.
type TunnelDirection string

const (
	TunnelLocal  TunnelDirection = "local"  // -L: local port → remote
	TunnelRemote TunnelDirection = "remote" // -R: remote port → local
)

// Tunnel describes a single port-forwarding rule.
type Tunnel struct {
	Direction  TunnelDirection `json:"direction"`
	LocalHost  string          `json:"local_host"`
	LocalPort  int             `json:"local_port"`
	RemoteHost string          `json:"remote_host"`
	RemotePort int             `json:"remote_port"`
}

// Group is a named folder that can contain profiles and other groups.
type Group struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	ParentID     string    `json:"parent_id,omitempty"`     // "" = root level
	PasswordHash string    `json:"password_hash,omitempty"` // bcrypt hash; empty = unlocked
	CreatedAt    time.Time `json:"created_at"`
}

// Locked reports whether the group is protected by a folder password.
func (g Group) Locked() bool { return g.PasswordHash != "" }

// CheckPassword reports whether password matches the group's folder
// password. An unprotected group accepts anything.
func (g Group) CheckPassword(password string) bool {
	if !g.Locked() {
		return true
	}
	return bcrypt.CompareHashAndPassword([]byte(g.PasswordHash), []byte(password)) == nil
}

// NewGroup returns a Group with a generated ID placed under parentID.
func NewGroup(parentID string) Group {
	return Group{
		ID:        uuid.New().String(),
		ParentID:  parentID,
		CreatedAt: time.Now(),
	}
}

// Profile holds all connection parameters for one server.
type Profile struct {
	ID          string    `json:"id"`
	GroupID     string    `json:"group_id,omitempty"` // "" = root level
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	User        string    `json:"user"`
	AuthType    AuthType  `json:"auth_type"`
	Password    string    `json:"password,omitempty"` // stored encrypted at rest
	KeyPath     string    `json:"key_path,omitempty"`
	KeyPassword string    `json:"key_password,omitempty"` // passphrase for the key
	Tunnels     []Tunnel  `json:"tunnels,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NewProfile returns a Profile with generated ID and timestamps.
func NewProfile() Profile {
	now := time.Now()
	return Profile{
		ID:        uuid.New().String(),
		Port:      22,
		AuthType:  AuthAgent,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Clone returns a deep copy of the profile.
func (p Profile) Clone() Profile {
	cp := p
	cp.Tunnels = slices.Clone(p.Tunnels)
	return cp
}

// vault is the plaintext structure stored (encrypted) on disk.
type vault struct {
	Version  int       `json:"version"`
	Groups   []Group   `json:"groups,omitempty"`
	Profiles []Profile `json:"profiles"`
}

// clone returns a copy whose slices can be modified without affecting v.
// Elements are values; a Profile's Tunnels slice is only ever replaced as a
// whole, never modified in place, so sharing it is safe.
func (v vault) clone() vault {
	return vault{
		Version:  v.Version,
		Groups:   slices.Clone(v.Groups),
		Profiles: slices.Clone(v.Profiles),
	}
}

// Store is the in-memory config, backed by an encrypted file.
type Store struct {
	path  string
	key   []byte // 32-byte AES key derived from master password
	salt  []byte // Argon2 salt, persisted in file header
	vault vault
}

// DefaultPath returns ~/.config/sshm/vault.enc
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "sshm")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "vault.enc"), nil
}

// Open loads (or creates) the encrypted vault at path using the given master password.
func Open(path string, masterPass []byte) (*Store, error) {
	s := &Store{path: path}

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read vault: %w", err)
	}

	if os.IsNotExist(err) || len(raw) == 0 {
		// New vault — generate salt, derive key, start empty.
		salt, err := generateSalt()
		if err != nil {
			return nil, err
		}
		s.salt = salt
		s.key = deriveKey(masterPass, salt)
		s.vault = vault{Version: 1, Profiles: []Profile{}}
		return s, nil
	}

	// Existing vault — parse header to get salt, derive key, decrypt.
	salt, ciphertext, err := parseFile(raw)
	if err != nil {
		return nil, fmt.Errorf("parse vault: %w", err)
	}
	s.salt = salt
	s.key = deriveKey(masterPass, salt)

	plaintext, err := decrypt(s.key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt vault (wrong password?): %w", err)
	}

	if err := json.Unmarshal(plaintext, &s.vault); err != nil {
		return nil, fmt.Errorf("unmarshal vault: %w", err)
	}
	return s, nil
}

// OpenWithKey opens an existing vault using an already-derived key (see
// SessionKey) instead of a password, skipping the expensive Argon2 step. It
// never creates a vault: a missing file is os.ErrNotExist. A key that no
// longer matches — the vault was re-keyed with `passwd` — fails
// authentication, so a stale cached key is harmless.
func OpenWithKey(path string, key []byte) (*Store, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vault: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("read vault: %w", os.ErrNotExist)
	}
	salt, ciphertext, err := parseFile(raw)
	if err != nil {
		return nil, fmt.Errorf("parse vault: %w", err)
	}
	plaintext, err := decrypt(key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt vault with cached key: %w", err)
	}
	s := &Store{path: path, salt: salt, key: slices.Clone(key)}
	if err := json.Unmarshal(plaintext, &s.vault); err != nil {
		return nil, fmt.Errorf("unmarshal vault: %w", err)
	}
	return s, nil
}

// SessionKey returns a copy of the derived encryption key, which is enough
// to reopen this vault via OpenWithKey without the master password. It's
// meant for a short-lived, session-scoped cache (see internal/keyring), not
// for persisting to disk.
func (s *Store) SessionKey() []byte { return slices.Clone(s.key) }

// Rekey re-encrypts the vault under a new master password: a fresh salt is
// generated and the file is rewritten in place. On failure the store keeps
// its old key, matching what is still on disk.
func (s *Store) Rekey(newMasterPass []byte) error {
	salt, err := generateSalt()
	if err != nil {
		return err
	}
	oldSalt, oldKey := s.salt, s.key
	s.salt, s.key = salt, deriveKey(newMasterPass, salt)
	if err := s.Save(); err != nil {
		s.salt, s.key = oldSalt, oldKey
		return err
	}
	return nil
}

// Path returns the vault file this store reads and writes.
func (s *Store) Path() string { return s.path }

// Save encrypts and writes the vault to disk atomically, with mode 0600.
func (s *Store) Save() error {
	plaintext, err := json.Marshal(s.vault)
	if err != nil {
		return err
	}
	ciphertext, err := encrypt(s.key, plaintext)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.path, buildFile(s.salt, ciphertext), 0o600)
}

// commit applies change to the in-memory vault and saves it. If change or
// the save fails, the in-memory state is rolled back, so what the UI shows
// never drifts from what is on disk.
func (s *Store) commit(change func() error) error {
	prev := s.vault.clone()
	if err := change(); err != nil {
		s.vault = prev
		return err
	}
	if err := s.Save(); err != nil {
		s.vault = prev
		return err
	}
	return nil
}

// Profiles returns a deep copy of all profiles (all groups).
func (s *Store) Profiles() []Profile {
	out := make([]Profile, len(s.vault.Profiles))
	for i, p := range s.vault.Profiles {
		out[i] = p.Clone()
	}
	return out
}

// ProfilesInGroup returns profiles that belong directly to groupID ("" = root).
func (s *Store) ProfilesInGroup(groupID string) []Profile {
	var out []Profile
	for _, p := range s.vault.Profiles {
		if p.GroupID == groupID {
			out = append(out, p.Clone())
		}
	}
	return out
}

// GroupsInGroup returns direct child groups of parentID ("" = root), sorted
// by name.
func (s *Store) GroupsInGroup(parentID string) []Group {
	var out []Group
	for _, g := range s.vault.Groups {
		if g.ParentID == parentID {
			out = append(out, g)
		}
	}
	slices.SortStableFunc(out, func(a, b Group) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// GetGroup returns a group by ID.
func (s *Store) GetGroup(id string) (Group, bool) {
	for _, g := range s.vault.Groups {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}

// GroupPath returns the chain of groups from root to id (inclusive), root
// first. It stops early at a missing parent.
func (s *Store) GroupPath(id string) []Group {
	var rev []Group
	for cur := id; cur != ""; {
		g, ok := s.GetGroup(cur)
		if !ok || slices.ContainsFunc(rev, func(x Group) bool { return x.ID == g.ID }) {
			break
		}
		rev = append(rev, g)
		cur = g.ParentID
	}
	slices.Reverse(rev)
	return rev
}

// AddGroup appends a new group and saves.
func (s *Store) AddGroup(g Group) error {
	if g.ID == "" {
		g.ID = uuid.New().String()
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	return s.commit(func() error {
		s.vault.Groups = append(s.vault.Groups, g)
		return nil
	})
}

// UpdateGroup replaces a group's editable fields (name) by ID and saves.
// The parent and the folder password are changed only by MoveGroup and
// SetGroupPassword/RemoveGroupPassword.
func (s *Store) UpdateGroup(g Group) error {
	return s.commit(func() error {
		i := slices.IndexFunc(s.vault.Groups, func(e Group) bool { return e.ID == g.ID })
		if i < 0 {
			return fmt.Errorf("group %s not found", g.ID)
		}
		s.vault.Groups[i].Name = g.Name
		return nil
	})
}

// DeleteGroup removes a group and all its descendants (sub-groups + profiles).
func (s *Store) DeleteGroup(id string) error {
	return s.commit(func() error {
		if _, ok := s.GetGroup(id); !ok {
			return fmt.Errorf("group %s not found", id)
		}
		doomed := s.subtree(id)
		s.vault.Groups = slices.DeleteFunc(s.vault.Groups, func(g Group) bool {
			_, ok := doomed[g.ID]
			return ok
		})
		s.vault.Profiles = slices.DeleteFunc(s.vault.Profiles, func(p Profile) bool {
			_, ok := doomed[p.GroupID]
			return ok
		})
		return nil
	})
}

// subtree returns the IDs of id and every group beneath it. It is computed
// up front, before anything is removed: deleting while walking the slice
// skipped children stored before their parent and left them (and their
// profiles) orphaned in the vault.
func (s *Store) subtree(id string) map[string]struct{} {
	ids := map[string]struct{}{id: {}}
	for grew := true; grew; {
		grew = false
		for _, g := range s.vault.Groups {
			if _, in := ids[g.ParentID]; in {
				if _, seen := ids[g.ID]; !seen {
					ids[g.ID] = struct{}{}
					grew = true
				}
			}
		}
	}
	return ids
}

// SetGroupPassword hashes password with bcrypt and stores it on the group.
func (s *Store) SetGroupPassword(id, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.setGroupHash(id, string(hash))
}

// RemoveGroupPassword clears the password on a group.
func (s *Store) RemoveGroupPassword(id string) error {
	return s.setGroupHash(id, "")
}

func (s *Store) setGroupHash(id, hash string) error {
	return s.commit(func() error {
		i := slices.IndexFunc(s.vault.Groups, func(g Group) bool { return g.ID == id })
		if i < 0 {
			return fmt.Errorf("group %s not found", id)
		}
		s.vault.Groups[i].PasswordHash = hash
		return nil
	})
}

// MoveProfile changes the GroupID of a profile ("" = root).
func (s *Store) MoveProfile(id, targetGroupID string) error {
	return s.commit(func() error {
		if err := s.checkTarget(targetGroupID); err != nil {
			return err
		}
		i := slices.IndexFunc(s.vault.Profiles, func(p Profile) bool { return p.ID == id })
		if i < 0 {
			return fmt.Errorf("profile %s not found", id)
		}
		s.vault.Profiles[i].GroupID = targetGroupID
		s.vault.Profiles[i].UpdatedAt = time.Now()
		return nil
	})
}

// MoveGroup changes the ParentID of a group ("" = root).
// Returns an error if the target would create a cycle.
func (s *Store) MoveGroup(id, targetParentID string) error {
	return s.commit(func() error {
		if err := s.checkTarget(targetParentID); err != nil {
			return err
		}
		if _, inside := s.subtree(id)[targetParentID]; inside {
			return fmt.Errorf("cannot move a folder into itself or its own descendant")
		}
		i := slices.IndexFunc(s.vault.Groups, func(g Group) bool { return g.ID == id })
		if i < 0 {
			return fmt.Errorf("group %s not found", id)
		}
		s.vault.Groups[i].ParentID = targetParentID
		return nil
	})
}

func (s *Store) checkTarget(groupID string) error {
	if groupID == "" {
		return nil
	}
	if _, ok := s.GetGroup(groupID); !ok {
		return fmt.Errorf("target folder %s not found", groupID)
	}
	return nil
}

// Get returns a profile by ID.
func (s *Store) Get(id string) (Profile, bool) {
	for _, p := range s.vault.Profiles {
		if p.ID == id {
			return p.Clone(), true
		}
	}
	return Profile{}, false
}

// Add appends a new profile and saves.
func (s *Store) Add(p Profile) error {
	p.CreatedAt = time.Now()
	p.UpdatedAt = p.CreatedAt
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return s.commit(func() error {
		s.vault.Profiles = append(s.vault.Profiles, p.Clone())
		return nil
	})
}

// AddBatch adds new groups and profiles with a single save — all or
// nothing. Used by imports, so a failure leaves no half-imported folders.
func (s *Store) AddBatch(groups []Group, profiles []Profile) error {
	now := time.Now()
	return s.commit(func() error {
		for _, g := range groups {
			if g.ID == "" {
				g.ID = uuid.New().String()
			}
			if g.CreatedAt.IsZero() {
				g.CreatedAt = now
			}
			s.vault.Groups = append(s.vault.Groups, g)
		}
		for _, p := range profiles {
			if err := s.checkTarget(p.GroupID); err != nil {
				return err
			}
			p.CreatedAt, p.UpdatedAt = now, now
			if p.ID == "" {
				p.ID = uuid.New().String()
			}
			s.vault.Profiles = append(s.vault.Profiles, p.Clone())
		}
		return nil
	})
}

// Update replaces a profile by ID and saves.
func (s *Store) Update(p Profile) error {
	return s.commit(func() error {
		i := slices.IndexFunc(s.vault.Profiles, func(e Profile) bool { return e.ID == p.ID })
		if i < 0 {
			return fmt.Errorf("profile %s not found", p.ID)
		}
		p.CreatedAt = s.vault.Profiles[i].CreatedAt
		p.UpdatedAt = time.Now()
		s.vault.Profiles[i] = p.Clone()
		return nil
	})
}

// Delete removes a profile by ID and saves.
func (s *Store) Delete(id string) error {
	return s.commit(func() error {
		n := len(s.vault.Profiles)
		s.vault.Profiles = slices.DeleteFunc(s.vault.Profiles, func(p Profile) bool { return p.ID == id })
		if len(s.vault.Profiles) == n {
			return fmt.Errorf("profile %s not found", id)
		}
		return nil
	})
}
