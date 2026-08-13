package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mussy/simple-ray/internal/domain"
)

type Store struct {
	Dir string
}

func New(dir string) *Store { return &Store{Dir: dir} }

func (s *Store) StatePath() string   { return filepath.Join(s.Dir, "state.json") }
func (s *Store) SecretsPath() string { return filepath.Join(s.Dir, "secrets.json") }
func (s *Store) BackupsDir() string  { return filepath.Join(s.Dir, "backups") }

func (s *Store) Ensure() error {
	if err := rejectSymlinkPath(s.Dir); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(s.Dir, 0700); err != nil {
		return fmt.Errorf("secure state directory: %w", err)
	}
	if err := os.MkdirAll(s.BackupsDir(), 0700); err != nil {
		return err
	}
	return os.Chmod(s.BackupsDir(), 0700)
}

func (s *Store) LoadState() (domain.State, error) {
	var value domain.State
	if err := readJSON(s.StatePath(), &value); err != nil {
		return value, err
	}
	if err := domain.ValidateState(value); err != nil {
		return value, fmt.Errorf("validate state: %w", err)
	}
	return value, nil
}

func (s *Store) SaveState(value domain.State) error {
	if err := domain.ValidateState(value); err != nil {
		return err
	}
	return s.writeJSON(s.StatePath(), value)
}

func (s *Store) LoadSecrets() (domain.Secrets, error) {
	var value domain.Secrets
	if err := readJSON(s.SecretsPath(), &value); err != nil {
		return value, err
	}
	if value.SchemaVersion != domain.SchemaVersion || value.APIToken == "" {
		return value, errors.New("secrets file is incomplete or incompatible")
	}
	return value, nil
}

func (s *Store) SaveSecrets(value domain.Secrets) error {
	if value.SchemaVersion != domain.SchemaVersion || value.APIToken == "" {
		return errors.New("refusing to save incomplete secrets")
	}
	return s.writeJSON(s.SecretsPath(), value)
}

func (s *Store) writeJSON(path string, value any) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink target %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".vpnctl-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func readJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing non-regular file %s", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("insecure permissions on %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return errors.New("unexpected trailing JSON data")
	}
	return nil
}

func rejectSymlinkPath(path string) error {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	root := volume + string(os.PathSeparator)
	if !filepath.IsAbs(clean) {
		root = "."
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink path component %s", current)
		}
	}
	return nil
}
