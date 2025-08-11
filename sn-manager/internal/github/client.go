//go:generate mockgen -destination=client_mock.go -package=github -source=client.go

package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GithubClient interface {
	GetLatestRelease() (*Release, error)
	ListReleases() ([]*Release, error)
	GetRelease(tag string) (*Release, error)
	GetSupernodeDownloadURL(version string) (string, error)
	DownloadBinary(url, destPath string, progress func(downloaded, total int64)) error
}

// Release represents a GitHub release
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	CreatedAt   time.Time `json:"created_at"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
	Body        string    `json:"body"`
}

// Asset represents a release asset
type Asset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
	ContentType string `json:"content_type"`
}

// Client handles GitHub API interactions
type Client struct {
	repo       string
	httpClient *http.Client
}

// NewClient creates a new GitHub API client
func NewClient(repo string) GithubClient {
	return &Client{
		repo: repo,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // Increased timeout for large binary downloads
		},
	}
}

// GetLatestRelease fetches the latest release from GitHub
func (c *Client) GetLatestRelease() (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "sn-manager")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error (status %d): %s", resp.StatusCode, string(body))
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &release, nil
}

// ListReleases fetches all releases from GitHub
func (c *Client) ListReleases() ([]*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases", c.repo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "sn-manager")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error (status %d): %s", resp.StatusCode, string(body))
	}

	var releases []*Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return releases, nil
}

// GetRelease fetches a specific release by tag
func (c *Client) GetRelease(tag string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", c.repo, tag)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "sn-manager")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error (status %d): %s", resp.StatusCode, string(body))
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &release, nil
}

// GetSupernodeDownloadURL returns the download URL for the supernode binary
func (c *Client) GetSupernodeDownloadURL(version string) (string, error) {
	// First try the direct download URL (newer releases)
	directURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/supernode-linux-amd64", c.repo, version)

	// Check if this URL exists
	resp, err := http.Head(directURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		return directURL, nil
	}

	// Fall back to checking release assets
	release, err := c.GetRelease(version)
	if err != nil {
		return "", err
	}

	// Look for the Linux binary in assets
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, "linux") && strings.Contains(asset.Name, "amd64") {
			return asset.DownloadURL, nil
		}
	}

	return "", fmt.Errorf("no Linux amd64 binary found for version %s", version)
}

// DownloadBinary downloads a binary from the given URL
func (c *Client) DownloadBinary(url, destPath string, progress func(downloaded, total int64)) error {
	// Create destination directory
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create temporary file
	tmpPath := destPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	// Download file
	resp, err := c.httpClient.Get(url)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		tmpFile.Close()
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Copy with progress reporting
	var written int64
	buf := make([]byte, 32*1024) // 32KB buffer
	total := resp.ContentLength

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil {
				tmpFile.Close()
				return fmt.Errorf("failed to write file: %w", writeErr)
			}
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			tmpFile.Close()
			return fmt.Errorf("download error: %w", err)
		}
	}

	// Close temp file before moving
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	// Move temp file to final destination
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to move file: %w", err)
	}

	// Make executable
	if err := os.Chmod(destPath, 0755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	return nil
}
