package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{"equal", "1.0.0", "1.0.0", 0},
		{"v prefix", "v1.0.0", "1.0.0", 0},
		{"a less major", "1.0.0", "2.0.0", -1},
		{"a greater major", "2.0.0", "1.0.0", 1},
		{"a less minor", "1.1.0", "1.2.0", -1},
		{"a greater minor", "1.2.0", "1.1.0", 1},
		{"a less patch", "1.0.1", "1.0.2", -1},
		{"a greater patch", "1.0.2", "1.0.1", 1},
		{"dev vs version", "dev", "1.0.0", -1},
		{"version vs dev", "1.0.0", "dev", 1},
		{"dev equal", "dev", "dev", 0},
		{"prerelease ignored", "1.0.0-beta", "1.0.0", 0},
		{"different lengths", "1.0", "1.0.0", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareVersions(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestBinaryAssetName(t *testing.T) {
	u := New(DefaultConfig())
	name, err := u.binaryAssetName(&Release{TagName: "v1.2.3"})
	if err != nil {
		t.Fatalf("binaryAssetName() error = %v", err)
	}

	// Should contain OS and arch
	if name == "" {
		t.Error("binaryAssetName() returned empty string")
	}
	expected := releaseAssetName("1.2.3")
	if name != expected {
		t.Errorf("binaryAssetName() = %q, want %q", name, expected)
	}
}

func TestSelectReleaseAssetsMatchesReleaseBinaryAsset(t *testing.T) {
	u := New(DefaultConfig())
	binaryName := releaseAssetName("1.2.3")
	release := &Release{TagName: "v1.2.3", Assets: []Asset{
		{Name: binaryName, BrowserDownloadURL: "https://example.com/binary", Size: 123},
		{Name: "SHA256SUMS", BrowserDownloadURL: "https://example.com/checksums"},
		{Name: "SHA256SUMS.sig", BrowserDownloadURL: "https://example.com/signature"},
	}}

	assets, err := u.selectReleaseAssets(release)
	if err != nil {
		t.Fatalf("selectReleaseAssets error: %v", err)
	}
	if assets.Binary.Name != binaryName {
		t.Fatalf("binary asset = %q, want %q", assets.Binary.Name, binaryName)
	}
	if !assetNameMatches(assets.Checksums.Name, checksumsAssetName) {
		t.Fatalf("checksums asset = %q, want %s", assets.Checksums.Name, checksumsAssetName)
	}
	if !assetNameMatches(assets.Signature.Name, signatureAssetName) {
		t.Fatalf("signature asset = %q, want %s", assets.Signature.Name, signatureAssetName)
	}
}

func TestSelectReleaseAssetsRejectsAmbiguousBinaryMatches(t *testing.T) {
	u := New(DefaultConfig())
	release := &Release{TagName: "v1.2.3", Assets: []Asset{
		{Name: releaseAssetName("1.2.3")},
		{Name: releaseAssetName("1.2.3")},
		{Name: "SHA256SUMS"},
		{Name: "SHA256SUMS.sig"},
	}}

	_, err := u.selectReleaseAssets(release)
	if err == nil || !strings.Contains(err.Error(), "multiple binary assets match") {
		t.Fatalf("selectReleaseAssets error = %v, want multiple binary assets match", err)
	}
}

func TestSelectReleaseAssetsRejectsWrongVersionBinaryMatch(t *testing.T) {
	u := New(DefaultConfig())
	release := &Release{TagName: "v1.2.3", Assets: []Asset{
		{Name: releaseAssetName("9.9.9")},
		{Name: "SHA256SUMS"},
		{Name: "SHA256SUMS.sig"},
	}}

	_, err := u.selectReleaseAssets(release)
	wantName := releaseAssetName("1.2.3")
	if err == nil || !strings.Contains(err.Error(), wantName) {
		t.Fatalf("selectReleaseAssets error = %v, want missing expected asset %q", err, wantName)
	}
}

func TestSelectReleaseAssetsRequiresCompanionAssets(t *testing.T) {
	u := New(DefaultConfig())
	binary := Asset{Name: releaseAssetName("1.2.3")}
	checksums := Asset{Name: "SHA256SUMS"}
	signature := Asset{Name: "SHA256SUMS.sig"}

	tests := []struct {
		name    string
		assets  []Asset
		wantErr string
	}{
		{
			name:    "missing binary",
			assets:  []Asset{checksums, signature},
			wantErr: "binary asset not found",
		},
		{
			name:    "missing checksums",
			assets:  []Asset{binary, signature},
			wantErr: "checksums asset not found",
		},
		{
			name:    "missing signature",
			assets:  []Asset{binary, checksums},
			wantErr: "signature asset not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := u.selectReleaseAssets(&Release{TagName: "v1.2.3", Assets: tt.assets})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("selectReleaseAssets error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyChecksumWithSelectedAssetUsesConcreteName(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "archive")
	content := []byte("selected binary archive")
	if err := os.WriteFile(archivePath, content, 0644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	selectedHash, err := calculateSHA256(archivePath)
	if err != nil {
		t.Fatalf("calculate selected hash: %v", err)
	}

	selectedName := releaseAssetName("1.2.3")
	otherName := releaseAssetName("9.9.9")
	checksumsPath := filepath.Join(tmpDir, "SHA256SUMS")
	checksums := strings.Repeat("0", 64) + "  " + otherName + "\n" +
		selectedHash + "  " + selectedName + "\n"
	if err := os.WriteFile(checksumsPath, []byte(checksums), 0644); err != nil {
		t.Fatalf("write checksums: %v", err)
	}

	if err := VerifyChecksum(archivePath, checksumsPath, selectedName); err != nil {
		t.Fatalf("VerifyChecksum with concrete selected asset error: %v", err)
	}
	wildcardAssetName := "caam_*_" + runtime.GOOS + "_" + runtime.GOARCH + "." + releaseAssetExt()
	if err := VerifyChecksum(archivePath, checksumsPath, wildcardAssetName); err == nil {
		t.Fatal("VerifyChecksum with wildcard pattern unexpectedly passed; regression setup is invalid")
	}
}

func TestUpdateForceBypassesNoUpdateEarlyReturn(t *testing.T) {
	currentVersion := strings.TrimPrefix(version.Short(), "v")
	client := releaseListClient(t, []Release{{
		TagName: "v" + currentVersion,
		HTMLURL: "https://example.com/current",
	}})

	u := New(Config{
		Owner:      "test",
		Repo:       "test",
		HTTPClient: client,
		Force:      true,
	})

	_, err := u.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "binary asset not found") {
		t.Fatalf("Update error = %v, want asset-selection error after force bypasses no-update return", err)
	}
}

func TestUpdateTargetVersionSkipsLatestPrecheck(t *testing.T) {
	var latestCalls int
	client := testHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/repos/test/repo/releases":
			latestCalls++
			return jsonHTTPResponse(http.StatusInternalServerError, `{"message":"latest should not be called"}`), nil
		case "/repos/test/repo/releases/tags/v1.2.3":
			return jsonHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.3","html_url":"https://example.com/v1.2.3","assets":[]}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
	}))

	u := New(Config{
		Owner:         "test",
		Repo:          "repo",
		TargetVersion: "1.2.3",
		HTTPClient:    client,
	})

	_, err := u.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "binary asset not found") {
		t.Fatalf("Update error = %v, want binary asset not found", err)
	}
	if latestCalls != 0 {
		t.Fatalf("latest release endpoint calls = %d, want 0", latestCalls)
	}
}

func TestUpdateRollbackRestoresBackupWhenReplaceFails(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", filepath.Join(tmpDir, "empty-path"))
	if err := os.MkdirAll(os.Getenv("PATH"), 0700); err != nil {
		t.Fatalf("mkdir empty PATH dir: %v", err)
	}

	exePath := filepath.Join(tmpDir, "caam")
	original := []byte("original binary")
	if err := os.WriteFile(exePath, original, 0755); err != nil {
		t.Fatalf("write exe: %v", err)
	}

	assetName := releaseAssetName("1.2.3")
	archiveBytes := releaseArchive(t, []byte("new binary"))
	archiveHash := sha256.Sum256(archiveBytes)
	checksums := fmt.Sprintf("%x  %s\n", archiveHash, assetName)

	release := Release{
		TagName: "v1.2.3",
		HTMLURL: "https://example.com/v1.2.3",
		Assets: []Asset{
			{Name: assetName, BrowserDownloadURL: "https://downloads.example/binary", Size: int64(len(archiveBytes))},
			{Name: checksumsAssetName, BrowserDownloadURL: "https://downloads.example/checksums"},
			{Name: signatureAssetName, BrowserDownloadURL: "https://downloads.example/signature"},
		},
	}
	client := testHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://api.github.com/repos/test/repo/releases/tags/v1.2.3":
			body, err := json.Marshal(release)
			if err != nil {
				return nil, err
			}
			return bytesHTTPResponse(http.StatusOK, body), nil
		case "https://downloads.example/binary":
			return bytesHTTPResponse(http.StatusOK, archiveBytes), nil
		case "https://downloads.example/checksums":
			return bytesHTTPResponse(http.StatusOK, []byte(checksums)), nil
		case "https://downloads.example/signature":
			return bytesHTTPResponse(http.StatusOK, []byte("signature")), nil
		default:
			return bytesHTTPResponse(http.StatusNotFound, []byte("not found")), nil
		}
	}))

	originalReplace := atomicReplaceBinary
	atomicReplaceBinary = func(src, dst string) error {
		if err := os.WriteFile(dst, []byte("corrupted during replace"), 0755); err != nil {
			return err
		}
		return errors.New("injected replace failure")
	}
	t.Cleanup(func() { atomicReplaceBinary = originalReplace })

	u := New(Config{
		Owner:         "test",
		Repo:          "repo",
		TargetVersion: "1.2.3",
		HTTPClient:    client,
		ExePath:       exePath,
		BackupDir:     tmpDir,
	})

	_, err := u.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("Update error = %v, want rolled back replace failure", err)
	}

	restored, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read exe after rollback: %v", err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("exe after rollback = %q, want original %q", restored, original)
	}
}

func releaseAssetName(version string) string {
	return "caam_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + "." + releaseAssetExt()
}

func releaseAssetExt() string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return ext
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHTTPClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}
}

func jsonHTTPResponse(statusCode int, body string) *http.Response {
	return bytesHTTPResponse(statusCode, []byte(body))
}

func bytesHTTPResponse(statusCode int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    statusCode,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func releaseArchive(t *testing.T, binary []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("caam.exe")
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write(binary); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("close zip: %v", err)
		}
		return buf.Bytes()
	}

	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	if err := tw.WriteHeader(&tar.Header{Name: "caam", Mode: 0755, Size: int64(len(binary))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatalf("write tar entry: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func releaseListClient(t *testing.T, releases []Release) *http.Client {
	t.Helper()
	return testHTTPClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/repos/test/test/releases" {
			return jsonHTTPResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		body, err := json.Marshal(releases)
		if err != nil {
			return nil, err
		}
		return jsonHTTPResponse(http.StatusOK, string(body)), nil
	}))
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Owner != DefaultOwner {
		t.Errorf("Owner = %q, want %q", cfg.Owner, DefaultOwner)
	}
	if cfg.Repo != DefaultRepo {
		t.Errorf("Repo = %q, want %q", cfg.Repo, DefaultRepo)
	}
	if cfg.Channel != ChannelStable {
		t.Errorf("Channel = %q, want %q", cfg.Channel, ChannelStable)
	}
	if cfg.HTTPClient == nil {
		t.Error("HTTPClient should not be nil")
	}
}

func TestNew(t *testing.T) {
	t.Run("with empty config", func(t *testing.T) {
		u := New(Config{})
		if u.config.Owner != DefaultOwner {
			t.Errorf("Owner = %q, want %q", u.config.Owner, DefaultOwner)
		}
		if u.config.Repo != DefaultRepo {
			t.Errorf("Repo = %q, want %q", u.config.Repo, DefaultRepo)
		}
		if u.config.HTTPClient == nil {
			t.Error("HTTPClient should not be nil")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		u := New(Config{Owner: "custom", Repo: "repo"})
		if u.config.Owner != "custom" {
			t.Errorf("Owner = %q, want %q", u.config.Owner, "custom")
		}
	})
}

func TestFetchLatestRelease(t *testing.T) {
	releases := []Release{
		{TagName: "v1.0.0", Prerelease: false, Draft: false},
		{TagName: "v0.9.0", Prerelease: false, Draft: false},
	}

	cfg := Config{
		Owner:      "test",
		Repo:       "test",
		Channel:    ChannelStable,
		HTTPClient: releaseListClient(t, releases),
	}
	u := New(cfg)

	ctx := context.Background()
	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		t.Fatalf("fetchLatestRelease error: %v", err)
	}
	if release.TagName != "v1.0.0" {
		t.Errorf("TagName = %q, want %q", release.TagName, "v1.0.0")
	}
}

func TestFetchLatestRelease_FiltersDrafts(t *testing.T) {
	releases := []Release{
		{TagName: "v2.0.0", Draft: true},
		{TagName: "v1.0.0", Draft: false, Prerelease: false},
	}

	u := New(Config{
		Owner:      "test",
		Repo:       "test",
		Channel:    ChannelStable,
		HTTPClient: releaseListClient(t, releases),
	})

	release, err := u.fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease error: %v", err)
	}
	if release.TagName != "v1.0.0" {
		t.Errorf("TagName = %q, want v1.0.0", release.TagName)
	}
}

func TestFetchLatestRelease_FiltersPrereleases(t *testing.T) {
	releases := []Release{
		{TagName: "v2.0.0-beta", Prerelease: true},
		{TagName: "v1.0.0", Prerelease: false},
	}

	u := New(Config{
		Owner:      "test",
		Repo:       "test",
		Channel:    ChannelStable,
		HTTPClient: releaseListClient(t, releases),
	})

	release, err := u.fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease error: %v", err)
	}
	if release.TagName != "v1.0.0" {
		t.Errorf("TagName = %q, want v1.0.0", release.TagName)
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		patternParts []string
		want         bool
	}{
		{
			name:         "exact match",
			filename:     "caam_1.0.0_linux_amd64.tar.gz",
			patternParts: []string{"caam_1.0.0_linux_amd64.tar.gz"},
			want:         true,
		},
		{
			name:         "wildcard match",
			filename:     "caam_1.0.0_linux_amd64.tar.gz",
			patternParts: []string{"caam_", "_linux_amd64.tar.gz"},
			want:         true,
		},
		{
			name:         "wildcard no match prefix",
			filename:     "other_1.0.0_linux_amd64.tar.gz",
			patternParts: []string{"caam_", "_linux_amd64.tar.gz"},
			want:         false,
		},
		{
			name:         "wildcard no match suffix",
			filename:     "caam_1.0.0_darwin_arm64.tar.gz",
			patternParts: []string{"caam_", "_linux_amd64.tar.gz"},
			want:         false,
		},
		{
			name:         "exact no match",
			filename:     "different.tar.gz",
			patternParts: []string{"caam.tar.gz"},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchPattern(tt.filename, tt.patternParts)
			if got != tt.want {
				t.Errorf("matchPattern(%q, %v) = %v, want %v",
					tt.filename, tt.patternParts, got, tt.want)
			}
		})
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "caam-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := []byte("test content")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Copy file
	dstPath := filepath.Join(tmpDir, "dest.txt")
	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}

	// Verify content
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", got, content)
	}
}

func TestCalculateSHA256(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "caam-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test file with known content
	testPath := filepath.Join(tmpDir, "test.txt")
	content := []byte("hello world")
	if err := os.WriteFile(testPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Expected SHA256 of "hello world"
	expected := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	got, err := calculateSHA256(testPath)
	if err != nil {
		t.Fatalf("calculateSHA256() error = %v", err)
	}
	if got != expected {
		t.Errorf("calculateSHA256() = %q, want %q", got, expected)
	}
}

func TestReadExpectedChecksum(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "caam-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create checksums file
	checksumsContent := `abc123  caam_1.0.0_linux_amd64.tar.gz
def456  caam_1.0.0_darwin_arm64.tar.gz
ghi789  SHA256SUMS.sig
`
	checksumsPath := filepath.Join(tmpDir, "SHA256SUMS")
	if err := os.WriteFile(checksumsPath, []byte(checksumsContent), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		assetName string
		wantHash  string
		wantErr   bool
	}{
		{
			name:      "exact match",
			assetName: "caam_1.0.0_linux_amd64.tar.gz",
			wantHash:  "abc123",
		},
		{
			name:      "wildcard match",
			assetName: "caam_*_linux_amd64.tar.gz",
			wantHash:  "abc123",
		},
		{
			name:      "not found",
			assetName: "caam_1.0.0_windows_amd64.zip",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readExpectedChecksum(checksumsPath, tt.assetName)
			if (err != nil) != tt.wantErr {
				t.Errorf("readExpectedChecksum() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.wantHash {
				t.Errorf("readExpectedChecksum() = %q, want %q", got, tt.wantHash)
			}
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "caam-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test file
	testPath := filepath.Join(tmpDir, "test.bin")
	content := []byte("test binary content")
	if err := os.WriteFile(testPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Calculate actual hash
	actualHash, err := calculateSHA256(testPath)
	if err != nil {
		t.Fatal(err)
	}

	// Create checksums file with correct hash
	checksumsContent := actualHash + "  test.bin\n"
	checksumsPath := filepath.Join(tmpDir, "SHA256SUMS")
	if err := os.WriteFile(checksumsPath, []byte(checksumsContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Should pass
	if err := VerifyChecksum(testPath, checksumsPath, "test.bin"); err != nil {
		t.Errorf("VerifyChecksum() error = %v, want nil", err)
	}

	// Create checksums file with wrong hash
	wrongChecksumsContent := "wronghash  test.bin\n"
	if err := os.WriteFile(checksumsPath, []byte(wrongChecksumsContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Should fail
	if err := VerifyChecksum(testPath, checksumsPath, "test.bin"); err == nil {
		t.Error("VerifyChecksum() should fail with wrong hash")
	}
}

func TestAtomicReplace(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "caam-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := filepath.Join(tmpDir, "new-binary")
	srcContent := []byte("new binary content")
	if err := os.WriteFile(srcPath, srcContent, 0755); err != nil {
		t.Fatal(err)
	}

	// Create destination file
	dstPath := filepath.Join(tmpDir, "old-binary")
	dstContent := []byte("old binary content")
	if err := os.WriteFile(dstPath, dstContent, 0755); err != nil {
		t.Fatal(err)
	}

	// Perform atomic replace
	if err := AtomicReplace(srcPath, dstPath); err != nil {
		t.Fatalf("AtomicReplace() error = %v", err)
	}

	// Verify destination has new content
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(srcContent) {
		t.Errorf("destination content = %q, want %q", got, srcContent)
	}
}

func TestAtomicReplace_CreatesDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "caam-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source file
	srcPath := filepath.Join(tmpDir, "source")
	if err := os.WriteFile(srcPath, []byte("content"), 0755); err != nil {
		t.Fatal(err)
	}

	// Destination in non-existent directory
	dstPath := filepath.Join(tmpDir, "subdir", "dest")

	if err := AtomicReplace(srcPath, dstPath); err != nil {
		t.Fatalf("AtomicReplace() error = %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(dstPath); err != nil {
		t.Errorf("destination file should exist: %v", err)
	}
}
