package installer

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	Version         = "v3.5.0"
	maxArtifactSize = 256 << 20
	maxExtractSize  = 768 << 20
	maxArchiveFiles = 256
)

type Artifact struct {
	Architecture string
	URL          string
	SHA256       string
}

var artifacts = map[string]Artifact{
	"amd64": {Architecture: "amd64", URL: "https://github.com/MHSanaei/3x-ui/releases/download/v3.5.0/x-ui-linux-amd64.tar.gz", SHA256: "684cde5996098dc9384878faa99ac13b341883ec79b81948b1900e29511ee498"},
	"arm64": {Architecture: "arm64", URL: "https://github.com/MHSanaei/3x-ui/releases/download/v3.5.0/x-ui-linux-arm64.tar.gz", SHA256: "0205f7d0ffbb8f3deae3b45c047f08622b6ec7d9ac670a880e0ae77bdadb7514"},
}

type Preflight struct {
	OSVersion    string
	Architecture string
	MemoryBytes  uint64
	StateExists  bool
}

type Installer struct {
	StatePath  string
	ProgramDir string
	ConfigDir  string
	HTTP       *http.Client
	EUID       func() int
	GOARCH     string
}

func New() *Installer {
	client := &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Scheme != "https" || !approvedDownloadHost(req.URL.Hostname()) {
			return errors.New("artifact redirect was refused")
		}
		return nil
	}
	return &Installer{StatePath: "/var/lib/vpnctl/state.json", ProgramDir: "/usr/local/x-ui", ConfigDir: "/etc/x-ui", HTTP: client, EUID: os.Geteuid, GOARCH: runtime.GOARCH}
}

func (i *Installer) Check() (Preflight, error) {
	var result Preflight
	if i.EUID == nil || i.EUID() != 0 {
		return result, errors.New("installation requires root")
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return result, fmt.Errorf("read operating system release: %w", err)
	}
	values := parseOSRelease(string(osRelease))
	if values["ID"] != "ubuntu" || (values["VERSION_ID"] != "22.04" && values["VERSION_ID"] != "24.04") {
		return result, fmt.Errorf("unsupported operating system %s %s", values["ID"], values["VERSION_ID"])
	}
	architecture := normalizeArchitecture(i.GOARCH)
	if architecture == "" {
		return result, fmt.Errorf("unsupported architecture %s", i.GOARCH)
	}
	memory, err := memoryBytes("/proc/meminfo")
	if err != nil {
		return result, err
	}
	if memory < 512<<20 {
		return result, fmt.Errorf("at least 512 MiB RAM is required")
	}
	_, stateErr := os.Lstat(i.StatePath)
	result.StateExists = stateErr == nil
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return result, stateErr
	}
	if !result.StateExists {
		for _, path := range []string{i.ProgramDir, i.ConfigDir} {
			if _, err := os.Lstat(path); err == nil {
				return result, fmt.Errorf("unmanaged existing installation found at %s", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return result, err
			}
		}
	}
	result.OSVersion, result.Architecture, result.MemoryBytes = values["VERSION_ID"], architecture, memory
	return result, nil
}

func (i *Installer) DownloadAndStage(ctx context.Context, architecture, parent string) (string, error) {
	artifact, ok := artifacts[architecture]
	if !ok {
		return "", errors.New("unsupported artifact architecture")
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("staging parent must be an existing non-symlink directory")
	}
	work, err := os.MkdirTemp(parent, ".vpnctl-install-*")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(work)
		}
	}()
	if err := os.Chmod(work, 0700); err != nil {
		return "", err
	}
	archivePath := filepath.Join(work, "x-ui.tar.gz")
	if err := i.download(ctx, artifact, archivePath); err != nil {
		return "", err
	}
	stage := filepath.Join(work, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		return "", err
	}
	if err := extractVerifiedArchive(archivePath, stage); err != nil {
		return "", err
	}
	root := filepath.Join(stage, "x-ui")
	for _, required := range []string{"x-ui", "x-ui.service.debian", filepath.Join("bin", "xray-linux-"+architecture)} {
		info, err := os.Lstat(filepath.Join(root, required))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("verified archive lacks required regular file %s", required)
		}
	}
	keep = true
	return root, nil
}

func (i *Installer) download(ctx context.Context, artifact Artifact, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := i.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("download pinned 3x-ui artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artifact server returned status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArtifactSize {
		return errors.New("artifact exceeds size limit")
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(resp.Body, maxArtifactSize+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxArtifactSize {
		return errors.New("artifact exceeds size limit")
	}
	if hex.EncodeToString(hasher.Sum(nil)) != artifact.SHA256 {
		return errors.New("artifact checksum mismatch")
	}
	return nil
}

func extractVerifiedArchive(source, destination string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(io.LimitReader(f, maxArtifactSize+1))
	if err != nil {
		return errors.New("artifact is not valid gzip")
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	seen := make(map[string]bool)
	var total int64
	for count := 0; ; count++ {
		if count >= maxArchiveFiles {
			return errors.New("artifact contains too many entries")
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("artifact tar stream is invalid")
		}
		name := filepath.ToSlash(filepath.Clean(strings.TrimSuffix(header.Name, "/")))
		canonical := name
		if header.Typeflag == tar.TypeDir {
			canonical += "/"
		}
		if canonical != header.Name || (name != "x-ui" && !strings.HasPrefix(name, "x-ui/")) || strings.ContainsAny(name, "\x00\r\n") || seen[name] {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		seen[name] = true
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsupported archive entry type for %q", name)
		}
		if header.Mode&07000 != 0 || header.Size < 0 || total+header.Size > maxExtractSize {
			return errors.New("unsafe archive mode or extracted size")
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if header.Mode&0100 != 0 {
			mode = 0755
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(tarReader, header.Size+1))
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil || n != header.Size {
			return errors.New("artifact entry is truncated")
		}
		total += n
	}
	return nil
}

func parseOSRelease(content string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		result[key] = strings.Trim(strings.TrimSpace(value), "\"")
	}
	return result
}

func normalizeArchitecture(value string) string {
	switch value {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return ""
	}
}

func memoryBytes(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read memory information: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		var kib uint64
		if _, err := fmt.Sscanf(line, "MemTotal: %d kB", &kib); err == nil && kib > 0 {
			return kib * 1024, nil
		}
	}
	return 0, errors.New("memory information is invalid")
}

func approvedDownloadHost(host string) bool {
	return host == "github.com" || host == "objects.githubusercontent.com" || host == "release-assets.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}
