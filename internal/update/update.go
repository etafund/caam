// Package update provides self-update functionality for caam.
// It fetches releases from GitHub, verifies signatures and checksums,
// and performs atomic binary replacement with rollback support.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
)

const (
	// DefaultOwner is the default GitHub repository owner.
	DefaultOwner = "Dicklesworthstone"
	// DefaultRepo is the default GitHub repository name.
	DefaultRepo = "coding_agent_account_manager"
	// GitHubAPIBase is the base URL for GitHub API.
	GitHubAPIBase = "https://api.github.com"

	maxGitHubReleaseResponseBytes = 4 << 20
	checksumsAssetName            = "SHA256SUMS"
	signatureAssetName            = "SHA256SUMS.sig"
)

// Channel represents an update channel.
type Channel string

const (
	// ChannelStable is the stable release channel.
	ChannelStable Channel = "stable"
	// ChannelBeta is the beta/pre-release channel.
	ChannelBeta Channel = "beta"
)

// Release represents a GitHub release.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
}

// Asset represents a release asset.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

type releaseAssets struct {
	Binary    *Asset
	Checksums *Asset
	Signature *Asset
}

// ReleaseManifest is the release metadata embedded in releases.
type ReleaseManifest struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
}

// UpdateResult represents the result of an update operation.
type UpdateResult struct {
	Updated      bool
	FromVersion  string
	ToVersion    string
	ReleaseURL   string
	BackupPath   string
	DownloadSize int64
}

// CheckResult represents the result of a version check.
type CheckResult struct {
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
	Release         *Release
	Channel         Channel
}

// Config holds update configuration.
type Config struct {
	// Owner is the GitHub repository owner.
	Owner string
	// Repo is the GitHub repository name.
	Repo string
	// Channel is the update channel (stable or beta).
	Channel Channel
	// TargetVersion pins to a specific version (optional).
	TargetVersion string
	// Force reinstalls the resolved release even when it is not newer.
	Force bool
	// HTTPClient is the HTTP client to use.
	HTTPClient *http.Client
	// ExePath is the path to the executable to update.
	// If empty, uses the current executable.
	ExePath string
	// BackupDir is where to store backup copies.
	// If empty, uses the same directory as the executable.
	BackupDir string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Owner:   DefaultOwner,
		Repo:    DefaultRepo,
		Channel: ChannelStable,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Updater handles self-updates.
type Updater struct {
	config Config
}

var atomicReplaceBinary = AtomicReplace

// New creates a new Updater with the given configuration.
func New(config Config) *Updater {
	if config.Owner == "" {
		config.Owner = DefaultOwner
	}
	if config.Repo == "" {
		config.Repo = DefaultRepo
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Updater{config: config}
}

// Check checks for available updates without downloading.
func (u *Updater) Check(ctx context.Context) (*CheckResult, error) {
	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}

	currentVersion := version.Short()
	latestVersion := strings.TrimPrefix(release.TagName, "v")
	updateAvailable := compareVersions(currentVersion, latestVersion) < 0

	return &CheckResult{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
		Release:         release,
		Channel:         u.config.Channel,
	}, nil
}

// Update performs the update if a newer version is available.
func (u *Updater) Update(ctx context.Context) (*UpdateResult, error) {
	currentVersion := version.Short()
	result := &UpdateResult{
		FromVersion: currentVersion,
	}

	var release *Release
	var err error
	if u.config.TargetVersion != "" {
		release, err = u.fetchRelease(ctx, u.config.TargetVersion)
		if err != nil {
			return nil, fmt.Errorf("fetch target release: %w", err)
		}
		result.ToVersion = strings.TrimPrefix(release.TagName, "v")
		result.ReleaseURL = release.HTMLURL
	} else {
		check, err := u.Check(ctx)
		if err != nil {
			return nil, err
		}

		result.ToVersion = check.LatestVersion
		result.ReleaseURL = check.Release.HTMLURL

		if !check.UpdateAvailable && !u.config.Force {
			return result, nil
		}
		release = check.Release
	}

	// Find the appropriate binary asset
	assets, err := u.selectReleaseAssets(release)
	if err != nil {
		return nil, err
	}

	// Download and verify
	exePath := u.config.ExePath
	if exePath == "" {
		exePath, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("get executable path: %w", err)
		}
		exePath, err = filepath.EvalSymlinks(exePath)
		if err != nil {
			return nil, fmt.Errorf("resolve symlinks: %w", err)
		}
	}

	// Create temp directory for download
	tmpDir, err := os.MkdirTemp("", "caam-update-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Download checksums and signature
	checksumsPath := filepath.Join(tmpDir, "SHA256SUMS")
	signaturePath := filepath.Join(tmpDir, "SHA256SUMS.sig")
	archivePath := filepath.Join(tmpDir, assets.Binary.Name)

	if err := u.downloadFile(ctx, assets.Checksums.BrowserDownloadURL, checksumsPath); err != nil {
		return nil, fmt.Errorf("download checksums: %w", err)
	}
	if err := u.downloadFile(ctx, assets.Signature.BrowserDownloadURL, signaturePath); err != nil {
		return nil, fmt.Errorf("download signature: %w", err)
	}

	// Verify signature
	if err := VerifySignature(ctx, checksumsPath, signaturePath, release.TagName, u.config.Owner, u.config.Repo); err != nil {
		return nil, fmt.Errorf("verify signature: %w", err)
	}

	// Download binary archive
	if err := u.downloadFile(ctx, assets.Binary.BrowserDownloadURL, archivePath); err != nil {
		return nil, fmt.Errorf("download binary: %w", err)
	}
	result.DownloadSize = assets.Binary.Size

	// Verify checksum
	if err := VerifyChecksum(archivePath, checksumsPath, assets.Binary.Name); err != nil {
		return nil, fmt.Errorf("verify checksum: %w", err)
	}

	// Extract binary from archive
	binaryPath := filepath.Join(tmpDir, "caam")
	if runtime.GOOS == "windows" {
		binaryPath = filepath.Join(tmpDir, "caam.exe")
	}
	if err := ExtractBinary(archivePath, binaryPath); err != nil {
		return nil, fmt.Errorf("extract binary: %w", err)
	}

	// Backup current binary
	backupDir := u.config.BackupDir
	if backupDir == "" {
		backupDir = filepath.Dir(exePath)
	}
	backupPath := filepath.Join(backupDir, fmt.Sprintf("caam.%s.backup", currentVersion))
	if err := copyFile(exePath, backupPath); err != nil {
		return nil, fmt.Errorf("backup current binary: %w", err)
	}
	result.BackupPath = backupPath

	// Atomic replace
	if err := atomicReplaceBinary(binaryPath, exePath); err != nil {
		// Attempt rollback
		if rbErr := copyFile(backupPath, exePath); rbErr != nil {
			return nil, fmt.Errorf("replace failed (%v) and rollback failed (%v)", err, rbErr)
		}
		return nil, fmt.Errorf("replace failed (rolled back): %w", err)
	}

	result.Updated = true
	return result, nil
}

func (u *Updater) selectReleaseAssets(release *Release) (*releaseAssets, error) {
	binaryAssetName, err := u.binaryAssetName(release)
	if err != nil {
		return nil, err
	}

	var selected releaseAssets
	for i := range release.Assets {
		asset := &release.Assets[i]
		switch {
		case assetNameMatches(asset.Name, binaryAssetName):
			if selected.Binary != nil {
				return nil, fmt.Errorf("multiple binary assets match %s", binaryAssetName)
			}
			selected.Binary = asset
		case assetNameMatches(asset.Name, checksumsAssetName):
			selected.Checksums = asset
		case assetNameMatches(asset.Name, signatureAssetName):
			selected.Signature = asset
		}
	}

	if selected.Binary == nil {
		return nil, fmt.Errorf("binary asset not found: %s", binaryAssetName)
	}
	if selected.Checksums == nil {
		return nil, fmt.Errorf("checksums asset not found: %s", checksumsAssetName)
	}
	if selected.Signature == nil {
		return nil, fmt.Errorf("signature asset not found: %s", signatureAssetName)
	}

	return &selected, nil
}

func assetNameMatches(name, expected string) bool {
	return strings.Compare(name, expected) == 0
}

// fetchLatestRelease fetches the latest release based on channel.
func (u *Updater) fetchLatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases", GitHubAPIBase, u.config.Owner, u.config.Repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.config.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxGitHubReleaseResponseBytes)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}

	// Filter based on channel
	for _, release := range releases {
		if release.Draft {
			continue
		}
		if u.config.Channel == ChannelStable && release.Prerelease {
			continue
		}
		return &release, nil
	}

	return nil, fmt.Errorf("no releases found for channel %s", u.config.Channel)
}

// fetchRelease fetches a specific release by tag.
func (u *Updater) fetchRelease(ctx context.Context, tag string) (*Release, error) {
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", GitHubAPIBase, u.config.Owner, u.config.Repo, tag)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.config.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release not found: %s", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxGitHubReleaseResponseBytes)).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	return &release, nil
}

// binaryAssetName returns the exact expected asset name for the release and current platform.
func (u *Updater) binaryAssetName(release *Release) (string, error) {
	releaseVersion := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if releaseVersion == "" {
		return "", fmt.Errorf("release tag is empty")
	}

	osName := runtime.GOOS
	archName := runtime.GOARCH

	ext := "tar.gz"
	if osName == "windows" {
		ext = "zip"
	}

	// Match goreleaser naming: caam_VERSION_OS_ARCH.ext
	return fmt.Sprintf("caam_%s_%s_%s.%s", releaseVersion, osName, archName, ext), nil
}

// downloadFile downloads a URL to a local file.
func (u *Updater) downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := u.config.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// compareVersions compares two semver-ish versions.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareVersions(a, b string) int {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")

	if a == b {
		return 0
	}
	if a == "dev" {
		return -1
	}
	if b == "dev" {
		return 1
	}

	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	for i := 0; i < len(partsA) && i < len(partsB); i++ {
		// Handle pre-release suffixes (e.g., "1.0.0-beta")
		cleanA := strings.Split(partsA[i], "-")[0]
		cleanB := strings.Split(partsB[i], "-")[0]

		var numA, numB int
		fmt.Sscanf(cleanA, "%d", &numA)
		fmt.Sscanf(cleanB, "%d", &numB)

		if numA < numB {
			return -1
		}
		if numA > numB {
			return 1
		}
	}

	if len(partsA) < len(partsB) {
		return -1
	}
	if len(partsA) > len(partsB) {
		return 1
	}

	return 0
}
