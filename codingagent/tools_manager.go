package codingagent

// Port of utils/tools-manager.ts: managed binary discovery and download for
// fd and ripgrep. System PATH lookup first; managed bin dir second; GitHub
// release download last (offline/Termux-aware).

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	toolsNetworkTimeoutMs  = 10_000
	toolsDownloadTimeoutMs = 120_000
)

func isOfflineModeEnabled() bool {
	value := os.Getenv("PI_OFFLINE")
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return value == "1" || lower == "true" || lower == "yes"
}

type toolConfig struct {
	name              string
	repo              string
	binaryName        string
	systemBinaryNames []string
	getAssetName      func(version, plat, architecture string) string
}

var managedTools = map[string]toolConfig{
	"fd": {
		name:              "fd",
		repo:              "sharkdp/fd",
		binaryName:        "fd",
		systemBinaryNames: []string{"fd", "fdfind"},
		getAssetName: func(version, plat, architecture string) string {
			archStr := "x86_64"
			if architecture == "arm64" {
				archStr = "aarch64"
			}
			switch plat {
			case "darwin":
				return fmt.Sprintf("fd-v%s-%s-apple-darwin.tar.gz", version, archStr)
			case "linux":
				return fmt.Sprintf("fd-v%s-%s-unknown-linux-musl.tar.gz", version, archStr)
			case "win32":
				return fmt.Sprintf("fd-v%s-%s-pc-windows-msvc.zip", version, archStr)
			}
			return ""
		},
	},
	"rg": {
		name:       "ripgrep",
		repo:       "BurntSushi/ripgrep",
		binaryName: "rg",
		getAssetName: func(version, plat, architecture string) string {
			archStr := "x86_64"
			if architecture == "arm64" {
				archStr = "aarch64"
			}
			switch plat {
			case "darwin":
				return fmt.Sprintf("ripgrep-%s-%s-apple-darwin.tar.gz", version, archStr)
			case "linux":
				return fmt.Sprintf("ripgrep-%s-%s-unknown-linux-musl.tar.gz", version, archStr)
			case "win32":
				return fmt.Sprintf("ripgrep-%s-%s-pc-windows-msvc.zip", version, archStr)
			}
			return ""
		},
	},
}

var termuxPackages = map[string]string{"fd": "fd-find", "rg": "ripgrep"}

// commandExists probes a command via --version.
func commandExists(cmd string) bool {
	probe := exec.Command(cmd, "--version")
	probe.Stdout = nil
	probe.Stderr = nil
	return probe.Run() == nil
}

// GetToolPath returns the tool path (managed dir, or the PATH command name).
func GetToolPath(tool string) string {
	config, ok := managedTools[tool]
	if !ok {
		return ""
	}

	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	localPath := filepath.Join(GetBinDir(), config.binaryName+suffix)
	if _, err := os.Stat(localPath); err == nil {
		return localPath
	}

	systemBinaryNames := config.systemBinaryNames
	if systemBinaryNames == nil {
		systemBinaryNames = []string{config.binaryName}
	}
	for _, name := range systemBinaryNames {
		if commandExists(name) {
			return name
		}
	}
	return ""
}

func platformName() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "windows":
		return "win32"
	default:
		return runtime.GOOS
	}
}

// GetLatestVersion resolves the latest release tag from the web redirect
// (no GitHub API quota).
func GetLatestVersion(repo string) (string, error) {
	client := &http.Client{
		Timeout: time.Duration(toolsNetworkTimeoutMs) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequest("GET", "https://github.com/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", AppName()+"-coding-agent")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)

	if response.StatusCode < 300 || response.StatusCode >= 400 {
		return "", fmt.Errorf("Failed to resolve latest %s release: HTTP %d without redirect", repo, response.StatusCode)
	}
	location := response.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("Failed to resolve latest %s release: HTTP %d without redirect", repo, response.StatusCode)
	}
	if !strings.Contains(location, "/releases/tag/") {
		return "", fmt.Errorf("Failed to resolve latest %s release: unexpected redirect to %s", repo, location)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("Failed to resolve latest %s release: unexpected redirect to %s", repo, location)
	}
	segments := strings.Split(parsed.Path, "/")
	tag := ""
	if len(segments) > 0 {
		tag = segments[len(segments)-1]
	}
	if tag == "" {
		return "", fmt.Errorf("Failed to resolve latest %s release: unexpected redirect to %s", repo, location)
	}
	if decoded, err := url.QueryUnescape(tag); err == nil {
		tag = decoded
	}
	return strings.TrimPrefix(tag, "v"), nil
}

// ToolStatus reports download progress to callers.
type ToolStatus struct {
	Type    string // "info" | "warning" | "error"
	Message string
}

// EnsureTool returns the tool path, downloading when missing.
func EnsureTool(tool string, onStatus func(ToolStatus)) (string, error) {
	if existing := GetToolPath(tool); existing != "" {
		return existing, nil
	}

	config, ok := managedTools[tool]
	if !ok {
		return "", fmt.Errorf("unknown tool %s", tool)
	}

	if isOfflineModeEnabled() {
		if onStatus != nil {
			onStatus(ToolStatus{Type: "warning", Message: config.name + " not found. Offline mode enabled, skipping download."})
		}
		return "", nil
	}

	if runtime.GOOS == "android" {
		pkgName := tool
		if mapped, ok := termuxPackages[tool]; ok {
			pkgName = mapped
		}
		if onStatus != nil {
			onStatus(ToolStatus{Type: "warning", Message: config.name + " not found. Install with: pkg install " + pkgName})
		}
		return "", nil
	}

	if onStatus != nil {
		onStatus(ToolStatus{Type: "info", Message: config.name + " not found. Downloading..."})
	}

	path, err := downloadTool(tool)
	if err != nil {
		return "", err
	}
	if onStatus != nil {
		onStatus(ToolStatus{Type: "info", Message: config.name + " installed to " + path})
	}
	return path, nil
}

func downloadTool(tool string) (string, error) {
	config := managedTools[tool]
	version, err := GetLatestVersion(config.repo)
	if err != nil {
		return "", err
	}
	assetName := config.getAssetName(version, platformName(), runtime.GOARCH)
	if assetName == "" {
		return "", fmt.Errorf("no prebuilt %s binary for %s/%s", config.name, platformName(), runtime.GOARCH)
	}

	// fd tags carry a v prefix; ripgrep tags do not.
	tag := version
	if tool == "fd" {
		tag = "v" + version
	}
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", config.repo, tag, assetName)

	if err := os.MkdirAll(GetBinDir(), 0o755); err != nil {
		return "", err
	}
	archivePath := filepath.Join(GetBinDir(), assetName)
	if err := downloadFile(downloadURL, archivePath); err != nil {
		return "", err
	}
	defer os.Remove(archivePath)

	extractDir, err := os.MkdirTemp(GetBinDir(), "extract-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(extractDir)

	if err := extractArchive(archivePath, extractDir, assetName); err != nil {
		return "", err
	}

	binaryFileName := config.binaryName
	if runtime.GOOS == "windows" {
		binaryFileName += ".exe"
	}
	binaryPath := findBinaryRecursively(extractDir, binaryFileName)
	if binaryPath == "" {
		return "", fmt.Errorf("Failed to extract %s: binary %s not found in archive", assetName, binaryFileName)
	}

	target := filepath.Join(GetBinDir(), binaryFileName)
	if err := os.Rename(binaryPath, target); err != nil {
		return "", err
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return "", err
	}
	return target, nil
}

func downloadFile(address, dest string) error {
	client := &http.Client{Timeout: time.Duration(toolsDownloadTimeoutMs) * time.Millisecond}
	response, err := client.Get(address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Download failed with HTTP %d: %s", response.StatusCode, address)
	}
	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, response.Body)
	return err
}

func extractArchive(archivePath, extractDir, assetName string) error {
	if strings.HasSuffix(assetName, ".tar.gz") {
		return extractTarGz(archivePath, extractDir)
	}
	if strings.HasSuffix(assetName, ".zip") {
		return extractZip(archivePath, extractDir)
	}
	return fmt.Errorf("Failed to extract %s: unsupported archive", assetName)
}

func extractTarGz(archivePath, extractDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(extractDir, header.Name)
		if !strings.HasPrefix(target, filepath.Clean(extractDir)+string(os.PathSeparator)) {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tarReader); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

func extractZip(archivePath, extractDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		target := filepath.Join(extractDir, file.Name)
		if !strings.HasPrefix(target, filepath.Clean(extractDir)+string(os.PathSeparator)) {
			continue
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(out, src)
		src.Close()
		out.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func findBinaryRecursively(rootDir, binaryFileName string) string {
	var stack []string
	stack = append(stack, rootDir)
	for len(stack) > 0 {
		currentDir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(currentDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			fullPath := filepath.Join(currentDir, entry.Name())
			if entry.Type().IsRegular() && entry.Name() == binaryFileName {
				return fullPath
			}
			if entry.IsDir() {
				stack = append(stack, fullPath)
			}
		}
	}
	return ""
}
