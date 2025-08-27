//go:generate go run go.uber.org/mock/mockgen -destination=client_mock.go -package=github -source=client.go

package github

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GithubClient interface {
	GetLatestRelease() (*Release, error)
	GetLatestStableRelease() (*Release, error)
	ListReleases() ([]*Release, error)
	GetRelease(tag string) (*Release, error)
	GetSupernodeDownloadURL(version string) (string, error)
	GetReleaseTarballURL(version string) (string, error)
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
	repo           string
	httpClient     *http.Client
	downloadClient *http.Client
}

const (
	// APITimeout is the timeout for GitHub API calls
	APITimeout = 60 * time.Second // API request limit for unauthenticated calls
	// DownloadTimeout is the timeout for binary downloads
	DownloadTimeout = 5 * time.Minute
	// GatewayTimeout is the timeout for gateway status checks
	GatewayTimeout = 5 * time.Second
)

// NewClient creates a new GitHub API client
func NewClient(repo string) GithubClient {
	return &Client{
		repo: repo,
		httpClient: &http.Client{
			Timeout: APITimeout,
		},
		downloadClient: &http.Client{
			Timeout: DownloadTimeout,
		},
	}
}

// newRequest sets common headers for GitHub API and asset requests
func (c *Client) newRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "sn-manager")
	return req, nil
}

// GetLatestRelease fetches the latest release from GitHub
func (c *Client) GetLatestRelease() (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.repo)

	req, err := c.newRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

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

	req, err := c.newRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

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

// GetLatestStableRelease fetches the latest stable (non-prerelease, non-draft) release from GitHub
func (c *Client) GetLatestStableRelease() (*Release, error) {
	// Try the latest release endpoint first (single API call)
	release, err := c.GetLatestRelease()
	if err == nil && !release.Draft && !release.Prerelease {
		return release, nil
	}

	// Fallback to listing all releases if latest is not stable
	releases, err := c.ListReleases()
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	// Filter for stable releases (not draft, not prerelease)
	for _, release := range releases {
		if !release.Draft && !release.Prerelease {
			return release, nil
		}
	}

	return nil, fmt.Errorf("no stable releases found")
}

// GetRelease fetches a specific release by tag
func (c *Client) GetRelease(tag string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", c.repo, tag)

	req, err := c.newRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

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

	// Check if this URL exists using our client (with timeout)
	req, err := c.newRequest("HEAD", directURL, nil)
	if err == nil {
		if resp, herr := c.httpClient.Do(req); herr == nil {
			// Accept 2xx and 3xx as existence (GitHub may redirect)
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				if err := resp.Body.Close(); err != nil {
					log.Printf("Warning: failed to close response body: %v", err)
				}
				return directURL, nil
			}
			io.Copy(io.Discard, resp.Body)
			if err := resp.Body.Close(); err != nil {
				log.Printf("Warning: failed to close response body: %v", err)
			}
		}
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

// GetReleaseTarballURL returns the download URL for the combined release tarball
// that includes both supernode and sn-manager binaries.
func (c *Client) GetReleaseTarballURL(version string) (string, error) {
	// Try direct URL first
	tarName := "supernode-linux-amd64.tar.gz"
	directURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", c.repo, version, tarName)

	if req, err := c.newRequest("HEAD", directURL, nil); err == nil {
		if resp, herr := c.httpClient.Do(req); herr == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				if err := resp.Body.Close(); err != nil {
					log.Printf("Warning: failed to close response body: %v", err)
				}
				return directURL, nil
			}
			io.Copy(io.Discard, resp.Body)
			if err := resp.Body.Close(); err != nil {
				log.Printf("Warning: failed to close response body: %v", err)
			}
		}
	}

	// Fallback to release assets lookup
	release, err := c.GetRelease(version)
	if err != nil {
		return "", err
	}
	for _, asset := range release.Assets {
		if asset.Name == tarName || (strings.Contains(asset.Name, "linux") && strings.HasSuffix(asset.Name, ".tar.gz")) {
			return asset.DownloadURL, nil
		}
	}
	return "", fmt.Errorf("no suitable tarball asset found for version %s", version)
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

	// Download file using request with headers
	req, err := c.newRequest("GET", url, nil)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := c.downloadClient.Do(req)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		tmpFile.Close()
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Copy with progress reporting using TeeReader
	total := resp.ContentLength
	if progress != nil {
		pr := &progressReporter{cb: progress, total: total}
		reader := io.TeeReader(resp.Body, pr)
		if _, err := io.Copy(tmpFile, reader); err != nil {
			tmpFile.Close()
			return fmt.Errorf("download error: %w", err)
		}
	} else {
		if _, err := io.Copy(tmpFile, resp.Body); err != nil {
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

// progressReporter reports progress to a callback while counting bytes
type progressReporter struct {
	cb      func(downloaded, total int64)
	total   int64
	written int64
}

func (p *progressReporter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	if p.cb != nil {
		p.cb(p.written, p.total)
	}
	return n, nil
}
