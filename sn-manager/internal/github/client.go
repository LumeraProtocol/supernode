package github

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type GithubClient interface {
	GetLatestRelease() (*Release, error)
	GetLatestStableRelease() (*Release, error)
	ListReleases() ([]*Release, error)
	GetRelease(tag string) (*Release, error)
	GetReleaseTarballURL(version string) (string, error)
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

const (
	// APITimeout is the timeout for GitHub API calls
	APITimeout = 60 * time.Second // API request limit for unauthenticated calls
)

// NewClient creates a new GitHub API client
func NewClient(repo string) GithubClient {
	return &Client{
		repo:       repo,
		httpClient: &http.Client{Timeout: APITimeout},
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
	if err == nil && !release.Draft && !release.Prerelease && !strings.Contains(release.TagName, "-") {
		return release, nil
	}

	// Fallback to listing all releases if latest is not stable
	releases, err := c.ListReleases()
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	// Filter for stable releases (not draft, not prerelease)
	for _, release := range releases {
		if !release.Draft && !release.Prerelease && !strings.Contains(release.TagName, "-") {
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

// (Deprecated) Direct supernode binary URL lookup removed. Use GetReleaseTarballURL.

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

	// Fallback to exact-named asset lookup
	release, err := c.GetRelease(version)
	if err != nil {
		return "", err
	}
	for _, asset := range release.Assets {
		if asset.Name == tarName {
			return asset.DownloadURL, nil
		}
	}
	return "", fmt.Errorf("no suitable tarball asset found for version %s", version)
}

// Download support removed from client; use utils.DownloadFile instead.
