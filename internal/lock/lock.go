package lock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

type owner struct {
	PID       int       `json:"pid"`
	Operation string    `json:"operation"`
	Nonce     string    `json:"nonce"`
	CreatedAt time.Time `json:"createdAt"`
}

type Lock struct {
	path  string
	nonce string
}

func Acquire(path, operation string) (*Lock, error) {
	if operation == "" {
		return nil, errors.New("operation is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := os.Mkdir(path, 0700); err == nil {
			nonce, err := randomNonce()
			if err != nil {
				os.Remove(path)
				return nil, err
			}
			current := owner{PID: os.Getpid(), Operation: operation, Nonce: nonce, CreatedAt: time.Now().UTC()}
			data, _ := json.Marshal(current)
			if err := os.WriteFile(filepath.Join(path, "owner.json"), data, 0600); err != nil {
				os.RemoveAll(path)
				return nil, err
			}
			return &Lock{path: path, nonce: nonce}, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		stale, description := staleOwner(path)
		if !stale {
			return nil, fmt.Errorf("another vpnctl mutation is active (%s)", description)
		}
		quarantine := path + ".stale-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := os.Rename(path, quarantine); err != nil {
			return nil, fmt.Errorf("recover stale lock: %w", err)
		}
		if err := os.RemoveAll(quarantine); err != nil {
			return nil, fmt.Errorf("remove stale lock: %w", err)
		}
	}
	return nil, errors.New("could not acquire mutation lock")
}

func (l *Lock) Release() error {
	data, err := os.ReadFile(filepath.Join(l.path, "owner.json"))
	if err != nil {
		return err
	}
	var current owner
	if json.Unmarshal(data, &current) != nil || current.Nonce != l.nonce || current.PID != os.Getpid() {
		return errors.New("lock ownership changed; refusing release")
	}
	return os.RemoveAll(l.path)
}

func staleOwner(path string) (bool, string) {
	data, err := os.ReadFile(filepath.Join(path, "owner.json"))
	if err != nil {
		info, statErr := os.Lstat(path)
		if statErr == nil && info.IsDir() && info.ModTime().Before(time.Now().Add(-time.Minute)) {
			return true, "old incomplete lock"
		}
		return false, "lock metadata is unreadable"
	}
	var current owner
	if json.Unmarshal(data, &current) != nil || current.PID < 1 || current.Nonce == "" {
		return false, "lock metadata is invalid"
	}
	if runtime.GOOS != "linux" {
		return false, fmt.Sprintf("pid %d", current.PID)
	}
	_, err = os.Stat(filepath.Join("/proc", strconv.Itoa(current.PID)))
	if errors.Is(err, os.ErrNotExist) {
		return true, fmt.Sprintf("stale pid %d", current.PID)
	}
	return false, fmt.Sprintf("pid %d, operation %s", current.PID, current.Operation)
}

func randomNonce() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
