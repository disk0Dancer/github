package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	appName        = "github"
	defaultProfile = "default"
)

// Profile is one named configuration profile.
type Profile struct {
	Properties map[string]string `json:"properties"`
	Secrets    map[string]string `json:"secrets"`
}

// Store is the local profile store for the generated CLI.
type Store struct {
	Active   string              `json:"active"`
	Profiles map[string]*Profile `json:"profiles"`
	path     string
}

// MaskedEntry is one config entry prepared for user-facing listing.
type MaskedEntry struct {
	Key    string
	Value  string
	Secret bool
}

// Load loads the local config store, creating an empty default profile if
// it does not exist.
func Load() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newStore(path), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var store Store
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	store.path = path
	store.ensureDefaults()
	return &store, nil
}

func newStore(path string) *Store {
	store := &Store{
		Active:   defaultProfile,
		Profiles: map[string]*Profile{},
		path:     path,
	}
	store.ensureDefaults()
	return store
}

func (s *Store) ensureDefaults() {
	if s.Profiles == nil {
		s.Profiles = map[string]*Profile{}
	}
	if strings.TrimSpace(s.Active) == "" {
		s.Active = defaultProfile
	}
	if _, ok := s.Profiles[s.Active]; !ok {
		s.Profiles[s.Active] = &Profile{
			Properties: map[string]string{},
			Secrets:    map[string]string{},
		}
	}
	for name, cfg := range s.Profiles {
		if cfg == nil {
			s.Profiles[name] = &Profile{
				Properties: map[string]string{},
				Secrets:    map[string]string{},
			}
			continue
		}
		if cfg.Properties == nil {
			cfg.Properties = map[string]string{}
		}
		if cfg.Secrets == nil {
			cfg.Secrets = map[string]string{}
		}
	}
}

// DefaultPath returns the config file path for the generated CLI.
func DefaultPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}
	return filepath.Join(configDir, appName, "config.json"), nil
}

// Path returns the on-disk config file path.
func (s *Store) Path() string {
	return s.path
}

// Save writes the config store to disk.
func (s *Store) Save() error {
	s.ensureDefaults()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing config: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// ActiveProfileName returns the active profile name.
func (s *Store) ActiveProfileName() string {
	s.ensureDefaults()
	return s.Active
}

// ActiveProfile returns the active profile, creating it if needed.
func (s *Store) ActiveProfile() *Profile {
	s.ensureDefaults()
	return s.Profiles[s.Active]
}

// ProfileNames returns profile names in sorted order.
func (s *Store) ProfileNames() []string {
	s.ensureDefaults()
	names := make([]string, 0, len(s.Profiles))
	for name := range s.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CreateProfile creates a named profile if it does not exist.
func (s *Store) CreateProfile(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("profile name must not be empty")
	}
	s.ensureDefaults()
	if _, exists := s.Profiles[name]; exists {
		return fmt.Errorf("profile %q already exists", name)
	}
	s.Profiles[name] = &Profile{
		Properties: map[string]string{},
		Secrets:    map[string]string{},
	}
	return nil
}

// UseProfile switches the active profile.
func (s *Store) UseProfile(name string) error {
	name = strings.TrimSpace(name)
	s.ensureDefaults()
	if _, exists := s.Profiles[name]; !exists {
		return fmt.Errorf("profile %q does not exist", name)
	}
	s.Active = name
	return nil
}

// Set stores a key/value pair in the active profile. Secret keys are
// masked in list output.
func (s *Store) Set(key, value string, secret bool) {
	key = strings.TrimSpace(key)
	cfg := s.ActiveProfile()
	delete(cfg.Properties, key)
	delete(cfg.Secrets, key)
	if secret {
		cfg.Secrets[key] = value
		return
	}
	cfg.Properties[key] = value
}

// Unset removes a key from the active profile.
func (s *Store) Unset(key string) bool {
	cfg := s.ActiveProfile()
	_, propertyExists := cfg.Properties[key]
	_, secretExists := cfg.Secrets[key]
	delete(cfg.Properties, key)
	delete(cfg.Secrets, key)
	return propertyExists || secretExists
}

// Get returns a config value from the active profile.
func (s *Store) Get(key string) (string, bool) {
	cfg := s.ActiveProfile()
	if value, ok := cfg.Secrets[key]; ok {
		return value, true
	}
	value, ok := cfg.Properties[key]
	return value, ok
}

// MaskedEntries returns active-profile entries sorted by key with secrets masked.
func (s *Store) MaskedEntries() []MaskedEntry {
	cfg := s.ActiveProfile()
	keys := make([]string, 0, len(cfg.Properties)+len(cfg.Secrets))
	for key := range cfg.Properties {
		keys = append(keys, key)
	}
	for key := range cfg.Secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	entries := make([]MaskedEntry, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := cfg.Secrets[key]; ok {
			entries = append(entries, MaskedEntry{Key: key, Value: "[secret]", Secret: true})
			continue
		}
		entries = append(entries, MaskedEntry{Key: key, Value: cfg.Properties[key], Secret: false})
	}
	return entries
}
