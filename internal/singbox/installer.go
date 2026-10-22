package singbox

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	InstallDir     = "/opt/vonet/bin/sinbox"
	BinaryPath     = "/opt/vonet/bin/sinbox/sing-box"
	MarkerPath     = "/opt/vonet/bin/sinbox/.installed"
	DefaultVersion = "1.14.0"
)

// Download mirrors (China-friendly first)
var downloadMirrors = []string{
	"https://github.com/SagerNet/sing-box/releases/download/v%s/sing-box-%s-%s-%s.tar.gz",
	"https://ghfast.top/https://github.com/SagerNet/sing-box/releases/download/v%s/sing-box-%s-%s-%s.tar.gz",
	"https://ghproxy.com/https://github.com/SagerNet/sing-box/releases/download/v%s/sing-box-%s-%s-%s.tar.gz",
}

// Status represents the sing-box installation status.
type Status struct {
	Installed  bool   `json:"installed"`
	BinaryPath string `json:"binary_path"`
	Version    string `json:"version"`
	InstallDir string `json:"install_dir"`
	Error      string `json:"error,omitempty"`
}

// CheckStatus checks if sing-box is properly installed.
// A binary merely placed in the directory does NOT count as installed.
// The user must go through ScanLocal() or Install() to register it.
func CheckStatus() Status {
	status := Status{
		BinaryPath: BinaryPath,
		InstallDir: InstallDir,
	}
	// Must have the marker file written by ScanLocal or Install.
	if _, err := os.Stat(MarkerPath); os.IsNotExist(err) {
		status.Installed = false
		if _, binErr := os.Stat(BinaryPath); binErr == nil {
			status.Error = "二进制文件已存在但未注册，请点击「本地扫描」或「远程下载」完成安装"
		}
		return status
	}
	// Marker exists — verify the binary still works.
	if _, err := os.Stat(BinaryPath); os.IsNotExist(err) {
		status.Installed = false
		status.Error = "标记文件存在但二进制丢失，请重新安装"
		return status
	}
	cmd := exec.Command(BinaryPath, "version")
	output, err := cmd.Output()
	if err != nil {
		status.Installed = false
		status.Error = fmt.Sprintf("二进制无法执行: %v", err)
		return status
	}
	versionStr := strings.TrimSpace(string(output))
	if versionStr == "" {
		status.Installed = false
		status.Error = "二进制返回空版本"
		return status
	}
	status.Installed = true
	lines := strings.SplitN(versionStr, "\n", 2)
	if len(lines) > 0 {
		status.Version = lines[0]
	}
	return status
}

// ScanLocal scans the install directory for a manually placed sing-box binary,
// verifies it works, and registers it (writes the marker file).
func ScanLocal() (Status, error) {
	status := Status{
		BinaryPath: BinaryPath,
		InstallDir: InstallDir,
	}
	// 1. Check binary exists.
	if _, err := os.Stat(BinaryPath); os.IsNotExist(err) {
		status.Error = fmt.Sprintf("未找到二进制文件: %s", BinaryPath)
		return status, fmt.Errorf("binary not found at %s", BinaryPath)
	}
	// 2. Make executable.
	if err := os.Chmod(BinaryPath, 0o755); err != nil {
		status.Error = fmt.Sprintf("chmod 失败: %v", err)
		return status, fmt.Errorf("chmod: %w", err)
	}
	// 3. Verify it runs.
	cmd := exec.Command(BinaryPath, "version")
	output, err := cmd.Output()
	if err != nil {
		status.Error = fmt.Sprintf("二进制无法执行: %v", err)
		return status, fmt.Errorf("binary not working: %w", err)
	}
	versionStr := strings.TrimSpace(string(output))
	if versionStr == "" {
		status.Error = "二进制返回空版本"
		return status, fmt.Errorf("empty version output")
	}
	// 4. Write marker file to register the installation.
	if err := writeMarker(versionStr); err != nil {
		status.Error = fmt.Sprintf("写入标记文件失败: %v", err)
		return status, err
	}
	status.Installed = true
	lines := strings.SplitN(versionStr, "\n", 2)
	if len(lines) > 0 {
		status.Version = lines[0]
	}
	return status, nil
}

// Install downloads and installs sing-box to the install directory.
func Install(version string, progressFn func(msg string)) error {
	if version == "" {
		version = DefaultVersion
	}
	if progressFn == nil {
		progressFn = func(string) {}
	}

	// Create install directory
	progressFn("创建安装目录: " + InstallDir)
	if err := os.MkdirAll(InstallDir, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}

	// Detect platform
	goos := runtime.GOOS
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported architecture: %s", arch)
	}

	// Try download mirrors
	var lastErr error
	for _, mirrorTpl := range downloadMirrors {
		url := fmt.Sprintf(mirrorTpl, version, version, goos, arch)
		progressFn("尝试下载: " + url)

		err := downloadAndExtract(url, InstallDir, progressFn)
		if err == nil {
			// Verify binary
			if _, statErr := os.Stat(BinaryPath); statErr != nil {
				return fmt.Errorf("binary not found after install at %s", BinaryPath)
			}
			if chmodErr := os.Chmod(BinaryPath, 0o755); chmodErr != nil {
				return fmt.Errorf("chmod: %w", chmodErr)
			}
			// Verify it runs and get version.
			cmd := exec.Command(BinaryPath, "version")
			output, verErr := cmd.Output()
			versionStr := "unknown"
			if verErr == nil {
				versionStr = strings.TrimSpace(string(output))
			}
			// Write marker file.
			if markerErr := writeMarker(versionStr); markerErr != nil {
				return fmt.Errorf("write marker: %w", markerErr)
			}
			progressFn("安装成功")
			return nil
		}
		lastErr = err
		progressFn("下载失败: " + err.Error() + "，尝试下一个镜像...")
	}

	return fmt.Errorf("all download mirrors failed, last error: %w\n\n手动安装方法:\n1. 下载 sing-box: https://github.com/SagerNet/sing-box/releases\n2. 解压后将 sing-box 二进制文件放到: %s\n3. 点击「本地扫描」完成注册", lastErr, BinaryPath)
}

func downloadAndExtract(url string, destDir string, progressFn func(string)) error {
	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 15 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	contentLength := resp.ContentLength
	progressFn(fmt.Sprintf("下载中... (大小: %.1f MB)", float64(contentLength)/1024/1024))

	// Read with progress tracking
	progressReader := &progressReadCloser{
		reader:       resp.Body,
		total:        contentLength,
		progressFn:   progressFn,
		lastProgress: time.Now(),
	}
	defer progressReader.Close()

	progressFn("解压中...")
	return extractTarGz(progressReader, destDir)
}

type progressReadCloser struct {
	reader       io.ReadCloser
	total        int64
	read         int64
	progressFn   func(string)
	lastProgress time.Time
}

func (pr *progressReadCloser) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)
	if pr.total > 0 && time.Since(pr.lastProgress) > 2*time.Second {
		pct := float64(pr.read) / float64(pr.total) * 100
		pr.progressFn(fmt.Sprintf("下载中... %.1f%% (%.1f/%.1f MB)", pct, float64(pr.read)/1024/1024, float64(pr.total)/1024/1024))
		pr.lastProgress = time.Now()
	}
	return n, err
}

func (pr *progressReadCloser) Close() error {
	return pr.reader.Close()
}

// Uninstall removes the sing-box installation.
func Uninstall() error {
	return os.RemoveAll(InstallDir)
}

// IsInstalled checks if the binary exists and is registered.
func IsInstalled() bool {
	if _, err := os.Stat(MarkerPath); err != nil {
		return false
	}
	_, err := os.Stat(BinaryPath)
	return err == nil
}

// writeMarker writes the .installed marker file with version and timestamp.
func writeMarker(version string) error {
	content := fmt.Sprintf("version=%s\ninstalled_at=%s\n", version, time.Now().UTC().Format(time.RFC3339))
	return os.WriteFile(MarkerPath, []byte(content), 0o644)
}

func extractTarGz(reader io.Reader, destDir string) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		name := header.Name
		if !strings.HasSuffix(name, "sing-box") && !strings.Contains(name, "sing-box") {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}

		destPath := filepath.Join(destDir, "sing-box")
		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		f.Close()
		return nil
	}
	return fmt.Errorf("sing-box binary not found in archive")
}