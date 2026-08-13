package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mussy/simple-ray/internal/domain"
)

type databaseSource []byte

func (d databaseSource) GetDatabase(_ context.Context, dst io.Writer, _ int64) error {
	_, err := dst.Write(d)
	return err
}

type databaseSourceFunc func(io.Writer) error

func (f databaseSourceFunc) GetDatabase(_ context.Context, dst io.Writer, _ int64) error {
	return f(dst)
}

func TestCreateRejectsInvalidDatabasePayload(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "backup.tar.gz")
	_, err := Create(context.Background(), databaseSource("not a sqlite database"), validBackupState(), validBackupSecrets(), destination, true)
	if err == nil {
		t.Fatal("backup accepted an invalid 3x-ui database payload")
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("invalid backup was published: %v", statErr)
	}
}

func TestCreateDoesNotOverwriteDestinationCreatedDuringBackup(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "backup.tar.gz")
	sentinel := []byte("pre-existing operator data")
	api := databaseSourceFunc(func(dst io.Writer) error {
		if err := os.WriteFile(destination, sentinel, 0600); err != nil {
			return err
		}
		_, err := dst.Write(sqliteFixture())
		return err
	})
	if _, err := Create(context.Background(), api, validBackupState(), validBackupSecrets(), destination, true); err == nil {
		t.Fatal("backup overwrote a destination created after the initial existence check")
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, sentinel) {
		t.Fatalf("destination changed: got %q", content)
	}
}

func TestReadRejectsDuplicateManifestEntries(t *testing.T) {
	stateData, err := json.Marshal(validBackupState())
	if err != nil {
		t.Fatal(err)
	}
	secretsData, err := json.Marshal(validBackupSecrets())
	if err != nil {
		t.Fatal(err)
	}
	database := sqliteFixture()
	databaseSum := sha256.Sum256(database)
	duplicate := File{Name: "3x-ui/database.db", Mode: 0600, Size: int64(len(database)), SHA256: hex.EncodeToString(databaseSum[:])}
	metadata := Metadata{
		FormatVersion: FormatVersion,
		SchemaVersion: domain.SchemaVersion,
		VPNCTLVersion: domain.ProductVersion,
		XUIVersion:    domain.XUIVersion,
		Architecture:  "amd64",
		CreatedAt:     time.Now().UTC(),
		Plaintext:     true,
		Files:         []File{duplicate, duplicate, duplicate},
	}
	metadataData, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "duplicate-manifest.tar.gz")
	writeArchive(t, archive, map[string][]byte{
		"metadata.json":       metadataData,
		"3x-ui/database.db":   database,
		"vpnctl/state.json":   stateData,
		"vpnctl/secrets.json": secretsData,
	})
	if _, err := Read(archive); err == nil {
		t.Fatal("backup accepted duplicate manifest entries that omit state and secrets")
	}
}

func TestReadRejectsInvalidDatabasePayload(t *testing.T) {
	stateData, err := json.Marshal(validBackupState())
	if err != nil {
		t.Fatal(err)
	}
	secretsData, err := json.Marshal(validBackupSecrets())
	if err != nil {
		t.Fatal(err)
	}
	database := []byte("not a sqlite database")
	contents := map[string][]byte{
		"3x-ui/database.db":   database,
		"vpnctl/state.json":   stateData,
		"vpnctl/secrets.json": secretsData,
	}
	metadata := Metadata{
		FormatVersion: FormatVersion,
		SchemaVersion: domain.SchemaVersion,
		VPNCTLVersion: domain.ProductVersion,
		XUIVersion:    domain.XUIVersion,
		Architecture:  "amd64",
		CreatedAt:     time.Now().UTC(),
		Plaintext:     true,
	}
	for _, name := range []string{"3x-ui/database.db", "vpnctl/state.json", "vpnctl/secrets.json"} {
		sum := sha256.Sum256(contents[name])
		metadata.Files = append(metadata.Files, File{Name: name, Mode: 0600, Size: int64(len(contents[name])), SHA256: hex.EncodeToString(sum[:])})
	}
	metadataData, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	contents["metadata.json"] = metadataData
	archive := filepath.Join(t.TempDir(), "invalid-database.tar.gz")
	writeArchive(t, archive, contents)
	if _, err := Read(archive); err == nil {
		t.Fatal("backup reader accepted a self-consistent archive with an invalid database")
	}
}

func writeArchive(t *testing.T, destination string, files map[string][]byte) {
	t.Helper()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"metadata.json", "3x-ui/database.db", "vpnctl/state.json", "vpnctl/secrets.json"} {
		content := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func sqliteFixture() []byte {
	data := make([]byte, 4096)
	copy(data, "SQLite format 3\x00")
	data[16], data[17] = 0x10, 0x00
	data[18], data[19] = 1, 1
	data[20] = 0
	data[21], data[22], data[23] = 64, 32, 32
	return data
}

func validBackupState() domain.State {
	return domain.State{
		SchemaVersion: domain.SchemaVersion,
		VPNCTLVersion: domain.ProductVersion,
		XUIVersion:    domain.XUIVersion,
		Architecture:  "amd64",
		PublicAddress: "203.0.113.1",
		InboundID:     1,
		InboundRemark: "vpnctl-vless-reality",
		ListenPort:    443,
		PanelPort:     853,
		PanelBasePath: "/adminpath",
		PanelListen:   "127.0.0.1",
		RealityTarget: "example.com:443",
		RealitySNI:    "example.com",
	}
}

func validBackupSecrets() domain.Secrets {
	return domain.Secrets{SchemaVersion: domain.SchemaVersion, APIToken: "token"}
}
