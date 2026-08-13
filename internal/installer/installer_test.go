package installer

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestSupportedUbuntuVersions(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{version: "22.04", want: true},
		{version: "24.04", want: true},
		{version: "26.04", want: true},
		{version: "20.04", want: false},
		{version: "25.10", want: false},
		{version: "26.10", want: false},
		{version: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			if got := supportedUbuntuVersion(test.version); got != test.want {
				t.Fatalf("supportedUbuntuVersion(%q) = %t, want %t", test.version, got, test.want)
			}
		})
	}
}

func TestExtractAcceptsUpstreamRootDirectoryEntry(t *testing.T) {
	source := filepath.Join(t.TempDir(), "archive.tar.gz")
	f, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name     string
		body     string
		mode     int64
		typeFlag byte
	}{
		{name: "x-ui/", mode: 0755, typeFlag: tar.TypeDir},
		{name: "x-ui/x-ui", body: "binary", mode: 0755, typeFlag: tar.TypeReg},
	}
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Typeflag: entry.typeFlag, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := extractVerifiedArchive(source, destination); err != nil {
		t.Fatalf("extract upstream layout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "x-ui", "x-ui")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	source := filepath.Join(t.TempDir(), "archive.tar.gz")
	f, _ := os.Create(source)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "x-ui/../../escape", Mode: 0644, Typeflag: tar.TypeReg, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	if err := extractVerifiedArchive(source, t.TempDir()); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}
