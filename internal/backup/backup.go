package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mussy/simple-ray/internal/domain"
)

const (
	FormatVersion = 1
	maxDatabase   = 128 << 20
	maxArchive    = 160 << 20
	maxFiles      = 5
)

type DatabaseAPI interface {
	GetDatabase(context.Context, io.Writer, int64) error
}

type File struct {
	Name   string `json:"name"`
	Mode   int64  `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Metadata struct {
	FormatVersion int       `json:"formatVersion"`
	SchemaVersion int       `json:"schemaVersion"`
	VPNCTLVersion string    `json:"vpnctlVersion"`
	XUIVersion    string    `json:"xuiVersion"`
	Architecture  string    `json:"architecture"`
	CreatedAt     time.Time `json:"createdAt"`
	Plaintext     bool      `json:"plaintext"`
	Files         []File    `json:"files"`
}

type Result struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Format  int    `json:"formatVersion"`
	Warning string `json:"warning,omitempty"`
}

type Bundle struct {
	Metadata Metadata
	State    domain.State
	Secrets  domain.Secrets
	Database []byte
}

func Create(ctx context.Context, api DatabaseAPI, installation domain.State, secrets domain.Secrets, destination string, plaintextAcknowledged bool) (Result, error) {
	if !plaintextAcknowledged {
		return Result{}, errors.New("portable backup contains secrets; pass --plaintext to acknowledge the root-only unencrypted archive")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return Result{}, err
	}
	if info, err := os.Lstat(destination); err == nil {
		return Result{}, fmt.Errorf("destination already exists (%s)", info.Mode().Type())
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	parent := filepath.Dir(destination)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Result{}, errors.New("backup destination parent must be an existing regular directory")
	}
	work, err := os.MkdirTemp(parent, ".vpnctl-backup-work-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)
	if err := os.Chmod(work, 0700); err != nil {
		return Result{}, err
	}
	dbPath := filepath.Join(work, "database.db")
	db, err := os.OpenFile(dbPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Result{}, err
	}
	if err := api.GetDatabase(ctx, db, maxDatabase); err != nil {
		db.Close()
		return Result{}, err
	}
	if err := db.Sync(); err != nil {
		db.Close()
		return Result{}, err
	}
	if err := db.Close(); err != nil {
		return Result{}, err
	}
	stateData, err := json.MarshalIndent(installation, "", "  ")
	if err != nil {
		return Result{}, err
	}
	secretData, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return Result{}, err
	}
	dbData, err := os.ReadFile(dbPath)
	if err != nil {
		return Result{}, err
	}
	if len(dbData) < 100 || !strings.HasPrefix(string(dbData[:16]), "SQLite format 3\x00") {
		return Result{}, errors.New("3x-ui returned an invalid SQLite database backup")
	}
	contents := map[string][]byte{
		"3x-ui/database.db":   dbData,
		"vpnctl/state.json":   append(stateData, '\n'),
		"vpnctl/secrets.json": append(secretData, '\n'),
	}
	metadata := Metadata{
		FormatVersion: FormatVersion,
		SchemaVersion: installation.SchemaVersion,
		VPNCTLVersion: installation.VPNCTLVersion,
		XUIVersion:    installation.XUIVersion,
		Architecture:  installation.Architecture,
		CreatedAt:     time.Now().UTC(),
		Plaintext:     true,
	}
	for name, content := range contents {
		sum := sha256.Sum256(content)
		metadata.Files = append(metadata.Files, File{Name: name, Mode: 0600, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])})
	}
	sort.Slice(metadata.Files, func(i, j int) bool { return metadata.Files[i].Name < metadata.Files[j].Name })
	metadataData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return Result{}, err
	}
	contents["metadata.json"] = append(metadataData, '\n')
	tmp, err := os.CreateTemp(parent, ".vpnctl-backup-*.tmp")
	if err != nil {
		return Result{}, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return Result{}, err
	}
	hasher := sha256.New()
	multi := io.MultiWriter(tmp, hasher)
	gz := gzip.NewWriter(multi)
	tarWriter := tar.NewWriter(gz)
	names := []string{"metadata.json", "3x-ui/database.db", "vpnctl/state.json", "vpnctl/secrets.json"}
	for _, name := range names {
		content := contents[name]
		header := &tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), ModTime: metadata.CreatedAt, Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			tmp.Close()
			return Result{}, err
		}
		if _, err := tarWriter.Write(content); err != nil {
			tmp.Close()
			return Result{}, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := gz.Close(); err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Result{}, err
	}
	stat, err := tmp.Stat()
	if err != nil {
		tmp.Close()
		return Result{}, err
	}
	if err := tmp.Close(); err != nil {
		return Result{}, err
	}
	if err := os.Link(tmpPath, destination); err != nil {
		return Result{}, err
	}
	if err := os.Remove(tmpPath); err != nil {
		return Result{}, err
	}
	directory, err := os.Open(parent)
	if err != nil {
		return Result{}, err
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return Result{}, err
	}
	if err := directory.Close(); err != nil {
		return Result{}, err
	}
	return Result{Path: destination, Size: stat.Size(), SHA256: hex.EncodeToString(hasher.Sum(nil)), Format: FormatVersion, Warning: "копия не зашифрована и содержит секреты"}, nil
}

func Read(source string) (Bundle, error) {
	var bundle Bundle
	if strings.Contains(source, "://") || source == "-" {
		return bundle, errors.New("restore source must be a local regular file")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return bundle, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxArchive {
		return bundle, errors.New("restore source is not an acceptable regular backup file")
	}
	f, err := os.Open(source)
	if err != nil {
		return bundle, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(io.LimitReader(f, maxArchive+1))
	if err != nil {
		return bundle, errors.New("backup is not a valid gzip archive")
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	allowed := map[string]bool{"metadata.json": true, "3x-ui/database.db": true, "vpnctl/state.json": true, "vpnctl/secrets.json": true}
	files := make(map[string][]byte)
	var total int64
	for count := 0; ; count++ {
		if count >= maxFiles {
			return bundle, errors.New("backup contains too many files")
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return bundle, errors.New("backup archive is truncated or invalid")
		}
		if header.Typeflag != tar.TypeReg || header.Name != filepath.ToSlash(filepath.Clean(header.Name)) || !allowed[header.Name] || files[header.Name] != nil || header.Mode&0177 != 0 {
			return bundle, fmt.Errorf("unsafe or unexpected backup member %q", header.Name)
		}
		if header.Size < 0 || header.Size > maxDatabase || total+header.Size > maxDatabase+(4<<20) {
			return bundle, errors.New("backup content exceeds size limit")
		}
		content, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(content)) != header.Size {
			return bundle, errors.New("backup member is truncated")
		}
		total += header.Size
		files[header.Name] = content
	}
	if len(files) != 4 {
		return bundle, errors.New("backup is incomplete")
	}
	if err := strictJSON(files["metadata.json"], &bundle.Metadata); err != nil {
		return bundle, fmt.Errorf("invalid backup metadata: %w", err)
	}
	if bundle.Metadata.FormatVersion != FormatVersion || bundle.Metadata.SchemaVersion != domain.SchemaVersion || !bundle.Metadata.Plaintext {
		return bundle, errors.New("backup format is unsupported")
	}
	if len(bundle.Metadata.Files) != 3 {
		return bundle, errors.New("backup manifest is incomplete")
	}
	expectedManifest := map[string]bool{
		"3x-ui/database.db":   true,
		"vpnctl/state.json":   true,
		"vpnctl/secrets.json": true,
	}
	declaredNames := make(map[string]bool, len(bundle.Metadata.Files))
	for _, declared := range bundle.Metadata.Files {
		if !expectedManifest[declared.Name] || declaredNames[declared.Name] {
			return bundle, fmt.Errorf("backup manifest has an unexpected or duplicate entry %q", declared.Name)
		}
		declaredNames[declared.Name] = true
		content, ok := files[declared.Name]
		if !ok || declared.Mode != 0600 || declared.Size != int64(len(content)) {
			return bundle, fmt.Errorf("backup manifest mismatch for %q", declared.Name)
		}
		sum := sha256.Sum256(content)
		if declared.SHA256 != hex.EncodeToString(sum[:]) {
			return bundle, fmt.Errorf("backup checksum mismatch for %q", declared.Name)
		}
	}
	if len(declaredNames) != len(expectedManifest) {
		return bundle, errors.New("backup manifest is incomplete")
	}
	database := files["3x-ui/database.db"]
	if len(database) < 100 || !strings.HasPrefix(string(database[:16]), "SQLite format 3\x00") {
		return bundle, errors.New("backup contains an invalid SQLite database")
	}
	if err := strictJSON(files["vpnctl/state.json"], &bundle.State); err != nil {
		return bundle, err
	}
	if err := strictJSON(files["vpnctl/secrets.json"], &bundle.Secrets); err != nil {
		return bundle, err
	}
	if err := domain.ValidateState(bundle.State); err != nil {
		return bundle, err
	}
	if bundle.Secrets.SchemaVersion != domain.SchemaVersion || bundle.Secrets.APIToken == "" {
		return bundle, errors.New("backup secrets are incomplete")
	}
	bundle.Database = database
	return bundle, nil
}

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}
