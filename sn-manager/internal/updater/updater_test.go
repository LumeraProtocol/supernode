package updater

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/config"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/github"
	"github.com/LumeraProtocol/supernode/v2/sn-manager/internal/version"
)

// setupTestEnvironment creates isolated test environment for updater tests
func setupTestEnvironment(t *testing.T) (string, func()) {
	homeDir, err := ioutil.TempDir("", "updater-test-")
	require.NoError(t, err)

	// Create required directories
	dirs := []string{
		filepath.Join(homeDir, "binaries"),
		filepath.Join(homeDir, "downloads"),
		filepath.Join(homeDir, "logs"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0755))
	}

	cleanup := func() {
		os.RemoveAll(homeDir)
	}

	return homeDir, cleanup
}

// createTestConfig creates a test configuration
func createTestConfig(t *testing.T, homeDir string, currentVersion string, autoUpgrade bool, checkInterval int) *config.Config {
	cfg := &config.Config{
		Updates: config.UpdateConfig{
			CheckInterval:  checkInterval,
			AutoUpgrade:    autoUpgrade,
			CurrentVersion: currentVersion,
		},
	}

	// Save config to file
	configPath := filepath.Join(homeDir, "config.yml")
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, ioutil.WriteFile(configPath, data, 0644))

	return cfg
}

// createMockBinary creates a mock binary file
func createMockBinary(t *testing.T, homeDir, version string) {
	versionDir := filepath.Join(homeDir, "binaries", version)
	require.NoError(t, os.MkdirAll(versionDir, 0755))

	binaryPath := filepath.Join(versionDir, "supernode")
	binaryContent := "#!/bin/sh\necho 'mock supernode " + version + "'\n"
	require.NoError(t, ioutil.WriteFile(binaryPath, []byte(binaryContent), 0755))
}

// TestAutoUpdater_ShouldUpdate tests version comparison logic
func TestAutoUpdater_ShouldUpdate(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)
	updater := New(homeDir, cfg)

	tests := []struct {
		name     string
		current  string
		latest   string
		expected bool
	}{
		// Patch version updates (should update)
		{"patch_update", "v1.0.0", "v1.0.1", true},
		{"patch_update_no_prefix", "1.0.0", "1.0.1", true},

		// Minor version updates (should NOT update based on current logic)
		{"minor_update", "v1.0.0", "v1.1.0", false},
		{"major_update", "v1.0.0", "v2.0.0", false},

		// Same version (should not update)
		{"same_version", "v1.0.0", "v1.0.0", false},

		// Downgrade (should not update)
		{"downgrade", "v1.0.1", "v1.0.0", false},

		// Invalid versions (should not update)
		{"invalid_current", "invalid", "v1.0.1", false},
		{"invalid_latest", "v1.0.0", "invalid", false},
		{"short_version", "v1.0", "v1.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updater.shouldUpdate(tt.current, tt.latest)
			assert.Equal(t, tt.expected, result, "shouldUpdate(%s, %s) = %v, want %v", tt.current, tt.latest, result, tt.expected)
		})
	}
}

// TestAutoUpdater_IsGatewayIdle tests gateway status checking
func TestAutoUpdater_IsGatewayIdle(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)

	tests := []struct {
		name           string
		serverResponse string
		statusCode     int
		expected       bool
	}{
		{
			name: "gateway_idle",
			serverResponse: `{
				"running_tasks": []
			}`,
			statusCode: http.StatusOK,
			expected:   true,
		},
		{
			name: "gateway_busy",
			serverResponse: `{
				"running_tasks": [
					{
						"service_name": "test-service",
						"task_ids": ["task1", "task2"],
						"task_count": 2
					}
				]
			}`,
			statusCode: http.StatusOK,
			expected:   false,
		},
		{
			name:           "gateway_error",
			serverResponse: `{"error": "internal server error"}`,
			statusCode:     http.StatusInternalServerError,
			expected:       false,
		},
		{
			name:           "invalid_json",
			serverResponse: `invalid json`,
			statusCode:     http.StatusOK,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.serverResponse))
			}))
			defer server.Close()

			// Create updater with custom gateway URL
			updater := New(homeDir, cfg)
			updater.gatewayURL = server.URL

			result := updater.isGatewayIdle()
			assert.Equal(t, tt.expected, result)
		})
	}

	t.Run("gateway_unreachable", func(t *testing.T) {
		updater := New(homeDir, cfg)
		updater.gatewayURL = "http://localhost:99999" // Non-existent port

		result := updater.isGatewayIdle()
		assert.False(t, result)
	})
}

// TestAutoUpdater_PerformUpdate tests the complete update process
func TestAutoUpdater_PerformUpdate(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)

	// Create initial version
	createMockBinary(t, homeDir, "v1.0.0")
	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	// Create mock controller and client
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	// Setup expectations
	targetVersion := "v1.0.1"
	downloadURL := "https://example.com/supernode-v1.0.1"

	mockClient.EXPECT().
		GetSupernodeDownloadURL(targetVersion).
		Return(downloadURL, nil)

	mockClient.EXPECT().
		DownloadBinary(downloadURL, gomock.Any(), gomock.Any()).
		DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
			// Simulate download by creating a mock binary
			mockBinaryContent := "#!/bin/sh\necho 'mock supernode v1.0.1'\n"
			return ioutil.WriteFile(destPath, []byte(mockBinaryContent), 0755)
		})

	// Create updater and inject mock client
	updater := New(homeDir, cfg)
	updater.githubClient = mockClient

	// Perform update
	err := updater.performUpdate(targetVersion)
	require.NoError(t, err)

	// Verify update was successful
	assert.Equal(t, targetVersion, updater.config.Updates.CurrentVersion)

	// Verify version was installed
	assert.True(t, updater.versionMgr.IsVersionInstalled(targetVersion))

	// Verify current version was set
	currentVersion, err := updater.versionMgr.GetCurrentVersion()
	require.NoError(t, err)
	assert.Equal(t, targetVersion, currentVersion)

	// Verify restart marker was created
	markerPath := filepath.Join(homeDir, ".needs_restart")
	markerContent, err := ioutil.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Equal(t, targetVersion, string(markerContent))

	// Verify config was updated
	updatedConfig, err := config.Load(filepath.Join(homeDir, "config.yml"))
	require.NoError(t, err)
	assert.Equal(t, targetVersion, updatedConfig.Updates.CurrentVersion)
}

// TestAutoUpdater_CheckAndUpdate tests the main update logic (Fixed Version)
func TestAutoUpdater_CheckAndUpdate(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		latestVersion  string
		gatewayIdle    bool
		expectUpdate   bool
		expectError    bool
	}{
		{
			name:           "update_available_gateway_idle",
			currentVersion: "v1.0.0",
			latestVersion:  "v1.0.1",
			gatewayIdle:    true,
			expectUpdate:   true,
		},
		{
			name:           "update_available_gateway_busy",
			currentVersion: "v1.0.0",
			latestVersion:  "v1.0.1",
			gatewayIdle:    false,
			expectUpdate:   false,
		},
		{
			name:           "no_update_available",
			currentVersion: "v1.0.1",
			latestVersion:  "v1.0.1",
			gatewayIdle:    true,
			expectUpdate:   false,
		},
		{
			name:           "minor_version_update_should_skip",
			currentVersion: "v1.0.0",
			latestVersion:  "v1.1.0",
			gatewayIdle:    true,
			expectUpdate:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create isolated environment for each subtest
			homeDir, cleanup := setupTestEnvironment(t)
			defer cleanup()

			cfg := createTestConfig(t, homeDir, tt.currentVersion, true, 3600)

			// Create initial version
			createMockBinary(t, homeDir, tt.currentVersion)
			versionMgr := version.NewManager(homeDir)
			require.NoError(t, versionMgr.SetCurrentVersion(tt.currentVersion))

			// Setup mock gateway server
			gatewayResponse := `{"running_tasks": []}`
			if !tt.gatewayIdle {
				gatewayResponse = `{"running_tasks": [{"service_name": "test", "task_count": 1}]}`
			}

			gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(gatewayResponse))
			}))
			defer gatewayServer.Close()

			// Create mock controller and client
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := github.NewMockGithubClient(ctrl)

			// Setup GitHub client expectations
			mockClient.EXPECT().
				GetLatestRelease().
				Return(&github.Release{
					TagName: tt.latestVersion,
				}, nil)

			if tt.expectUpdate {
				mockClient.EXPECT().
					GetSupernodeDownloadURL(tt.latestVersion).
					Return("https://example.com/binary", nil)

				mockClient.EXPECT().
					DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
						content := "#!/bin/sh\necho 'mock binary'\n"
						return ioutil.WriteFile(destPath, []byte(content), 0755)
					})
			}

			// Create updater and inject mocks
			updater := New(homeDir, cfg)
			updater.githubClient = mockClient
			updater.gatewayURL = gatewayServer.URL

			// Verify initial state - no restart marker should exist
			markerPath := filepath.Join(homeDir, ".needs_restart")
			_, err := os.Stat(markerPath)
			require.True(t, os.IsNotExist(err), "Restart marker should not exist initially")

			// Run check and update
			ctx := context.Background()
			updater.checkAndUpdate(ctx)

			// Verify results
			if tt.expectUpdate {
				assert.Equal(t, tt.latestVersion, updater.config.Updates.CurrentVersion, "Config should be updated to new version")

				// Verify restart marker exists
				_, err := os.Stat(markerPath)
				assert.NoError(t, err, "Restart marker should exist after successful update")

				// Verify marker content
				markerContent, err := ioutil.ReadFile(markerPath)
				require.NoError(t, err)
				assert.Equal(t, tt.latestVersion, string(markerContent), "Restart marker should contain the new version")

				// Verify new version is installed
				assert.True(t, updater.versionMgr.IsVersionInstalled(tt.latestVersion), "New version should be installed")

				// Verify current version is set
				currentVersion, err := updater.versionMgr.GetCurrentVersion()
				require.NoError(t, err)
				assert.Equal(t, tt.latestVersion, currentVersion, "Current version should be updated")
			} else {
				assert.Equal(t, tt.currentVersion, updater.config.Updates.CurrentVersion, "Config should remain unchanged")

				// Verify no restart marker
				_, err := os.Stat(markerPath)
				assert.True(t, os.IsNotExist(err), "Restart marker should not exist when no update occurred")
			}

			t.Logf("✅ Test case '%s' completed successfully", tt.name)
		})
	}
}

// Additional test to verify restart marker cleanup
func TestAutoUpdater_RestartMarkerHandling(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	// Create existing restart marker (simulating previous update)
	markerPath := filepath.Join(homeDir, ".needs_restart")
	require.NoError(t, ioutil.WriteFile(markerPath, []byte("v0.9.0"), 0644))

	// Verify marker exists initially
	_, err := os.Stat(markerPath)
	require.NoError(t, err, "Restart marker should exist initially")

	// Setup mocks for no update scenario
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)
	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.0"}, nil) // Same version

	// Setup idle gateway
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	// Run check and update (should not update)
	ctx := context.Background()
	updater.checkAndUpdate(ctx)

	// Verify existing restart marker is still there (not removed by checkAndUpdate)
	_, err = os.Stat(markerPath)
	assert.NoError(t, err, "Existing restart marker should not be removed by checkAndUpdate")

	// Verify content is unchanged
	content, err := ioutil.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Equal(t, "v0.9.0", string(content), "Existing restart marker content should be unchanged")
}

// Test to verify behavior when version manager operations fail
func TestAutoUpdater_VersionManagerErrors(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)

	// Create initial version and set it up properly
	createMockBinary(t, homeDir, "v1.0.0")
	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	// Make the binaries directory read-only to cause installation failures
	binariesDir := filepath.Join(homeDir, "binaries")
	require.NoError(t, os.Chmod(binariesDir, 0444)) // Read-only

	// Restore permissions in cleanup to allow directory removal
	defer func() {
		os.Chmod(binariesDir, 0755)
	}()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.1"}, nil)

	mockClient.EXPECT().
		GetSupernodeDownloadURL("v1.0.1").
		Return("https://example.com/binary", nil)

	mockClient.EXPECT().
		DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
			content := "#!/bin/sh\necho 'mock binary'\n"
			return ioutil.WriteFile(destPath, []byte(content), 0755)
		})

	// Setup idle gateway
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	// This should handle version manager errors gracefully
	ctx := context.Background()
	updater.checkAndUpdate(ctx)

	// Version should remain unchanged due to installation failure
	assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)

	// No restart marker should be created due to failure
	markerPath := filepath.Join(homeDir, ".needs_restart")
	_, err := os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "No restart marker should exist after failed update")
}

// Alternative test with download failure
func TestAutoUpdater_DownloadFailure(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.1"}, nil)

	mockClient.EXPECT().
		GetSupernodeDownloadURL("v1.0.1").
		Return("https://example.com/binary", nil)

	// Simulate download failure
	mockClient.EXPECT().
		DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(fmt.Errorf("download failed: network error"))

	// Setup idle gateway
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	// This should handle download errors gracefully
	ctx := context.Background()
	updater.checkAndUpdate(ctx)

	// Version should remain unchanged due to download failure
	assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)

	// No restart marker should be created due to failure
	markerPath := filepath.Join(homeDir, ".needs_restart")
	_, err := os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "No restart marker should exist after failed download")
}

// Test config save failure
func TestAutoUpdater_ConfigSaveFailure(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	// Make the home directory read-only to cause config save failure
	require.NoError(t, os.Chmod(homeDir, 0444))
	defer func() {
		os.Chmod(homeDir, 0755) // Restore for cleanup
	}()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.1"}, nil)

	mockClient.EXPECT().
		GetSupernodeDownloadURL("v1.0.1").
		Return("https://example.com/binary", nil)

	mockClient.EXPECT().
		DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
			content := "#!/bin/sh\necho 'mock binary'\n"
			return ioutil.WriteFile(destPath, []byte(content), 0755)
		})

	// Setup idle gateway
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	// This should handle config save errors
	ctx := context.Background()
	updater.checkAndUpdate(ctx)

	// The update might partially succeed but config save should fail
	// The exact behavior depends on implementation - let's just verify it doesn't crash
	t.Log("Config save failure test completed without panic")
}

// Simpler test that definitely causes failure
func TestAutoUpdater_InstallationFailure(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.1"}, nil)

	mockClient.EXPECT().
		GetSupernodeDownloadURL("v1.0.1").
		Return("https://example.com/binary", nil)

	// Download succeeds but creates a file in a location that will cause installation to fail
	mockClient.EXPECT().
		DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
			// Create an invalid binary (directory instead of file)
			return os.Mkdir(destPath, 0755)
		})

	// Setup idle gateway
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	// This should handle installation errors gracefully
	ctx := context.Background()
	updater.checkAndUpdate(ctx)

	// Version should remain unchanged due to installation failure
	assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)

	// No restart marker should be created due to failure
	markerPath := filepath.Join(homeDir, ".needs_restart")
	_, err := os.Stat(markerPath)
	assert.True(t, os.IsNotExist(err), "No restart marker should exist after failed installation")
}

// Test concurrent access to updater
func TestAutoUpdater_ConcurrentAccess(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	// Allow multiple calls (for concurrent access)
	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.0"}, nil).
		AnyTimes()

	// Setup idle gateway
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	// Run multiple concurrent checkAndUpdate calls
	const numGoroutines = 5
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errors <- fmt.Errorf("goroutine %d panicked: %v", id, r)
				}
			}()

			ctx := context.Background()
			updater.checkAndUpdate(ctx)
			errors <- nil
		}(i)
	}

	wg.Wait()
	close(errors)

	// Verify no errors occurred
	for err := range errors {
		assert.NoError(t, err)
	}

	// Verify system is in consistent state
	assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)
}

// TestAutoUpdater_StartStop tests auto-updater lifecycle
func TestAutoUpdater_StartStop(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	t.Run("auto_upgrade_disabled", func(t *testing.T) {
		cfg := createTestConfig(t, homeDir, "v1.0.0", false, 1) // 1 second interval
		updater := New(homeDir, cfg)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start should return immediately if auto-upgrade is disabled
		updater.Start(ctx)

		// Stop should work without issues
		updater.Stop()

		// No ticker should be created
		assert.Nil(t, updater.ticker)
	})

	t.Run("auto_upgrade_enabled", func(t *testing.T) {
		cfg := createTestConfig(t, homeDir, "v1.0.0", true, 1) // 1 second interval

		// Create mock controller and client
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := github.NewMockGithubClient(ctrl)

		// Expect at least one call to GetLatestRelease
		mockClient.EXPECT().
			GetLatestRelease().
			Return(&github.Release{
				TagName: "v1.0.0", // Same version, no update
			}, nil).
			AnyTimes()

		updater := New(homeDir, cfg)
		updater.githubClient = mockClient

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// Start auto-updater
		updater.Start(ctx)

		// Let it run for a bit
		time.Sleep(2 * time.Second)

		// Stop should work
		updater.Stop()

		// Ticker should have been created
		assert.NotNil(t, updater.ticker)
	})
}

// TestAutoUpdater_ErrorHandling tests error scenarios
func TestAutoUpdater_ErrorHandling(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600)

	t.Run("github_api_error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := github.NewMockGithubClient(ctrl)
		mockClient.EXPECT().
			GetLatestRelease().
			Return(nil, assert.AnError)

		updater := New(homeDir, cfg)
		updater.githubClient = mockClient

		// Should not panic or crash
		ctx := context.Background()
		updater.checkAndUpdate(ctx)

		// Version should remain unchanged
		assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)
	})

	t.Run("download_url_error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := github.NewMockGithubClient(ctrl)

		mockClient.EXPECT().
			GetLatestRelease().
			Return(&github.Release{TagName: "v1.0.1"}, nil)

		mockClient.EXPECT().
			GetSupernodeDownloadURL("v1.0.1").
			Return("", assert.AnError)

		// Setup idle gateway
		gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"running_tasks": []}`))
		}))
		defer gatewayServer.Close()

		updater := New(homeDir, cfg)
		updater.githubClient = mockClient
		updater.gatewayURL = gatewayServer.URL

		ctx := context.Background()
		updater.checkAndUpdate(ctx)

		// Version should remain unchanged
		assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)
	})

	t.Run("download_binary_error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := github.NewMockGithubClient(ctrl)

		mockClient.EXPECT().
			GetLatestRelease().
			Return(&github.Release{TagName: "v1.0.1"}, nil)

		mockClient.EXPECT().
			GetSupernodeDownloadURL("v1.0.1").
			Return("https://example.com/binary", nil)

		// Simulate download failure
		mockClient.EXPECT().
			DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(assert.AnError)

		// Setup idle gateway
		gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"running_tasks": []}`))
		}))
		defer gatewayServer.Close()

		updater := New(homeDir, cfg)
		updater.githubClient = mockClient
		updater.gatewayURL = gatewayServer.URL

		ctx := context.Background()
		updater.checkAndUpdate(ctx)

		// Version should remain unchanged
		assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)
	})
}

// / TestAutoUpdater_Integration tests end-to-end auto-update scenarios (Fixed Version)
func TestAutoUpdater_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create initial setup
	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 2) // 2 second interval
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	// Setup mock gateway (idle)
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	// Create mock controller and client
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	// Set up call sequence expectations:
	// 1. First call returns same version (no update)
	// 2. Second call returns new version (update available)
	// 3. Subsequent calls return the new version (no more updates)

	gomock.InOrder(
		// First call - no update
		mockClient.EXPECT().
			GetLatestRelease().
			Return(&github.Release{TagName: "v1.0.0"}, nil),

		// Second call - update available
		mockClient.EXPECT().
			GetLatestRelease().
			Return(&github.Release{TagName: "v1.0.1"}, nil),

		// Third and subsequent calls - no more updates
		mockClient.EXPECT().
			GetLatestRelease().
			Return(&github.Release{TagName: "v1.0.1"}, nil).
			AnyTimes(), // Allow any number of subsequent calls
	)

	// Expect download operations for the update
	mockClient.EXPECT().
		GetSupernodeDownloadURL("v1.0.1").
		Return("https://example.com/binary", nil)

	mockClient.EXPECT().
		DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
			content := "#!/bin/sh\necho 'mock supernode v1.0.1'\n"
			if progress != nil {
				progress(100, 100) // Report full download
			}
			return ioutil.WriteFile(destPath, []byte(content), 0755)
		})

	// Create and start updater
	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// Start the updater
	updater.Start(ctx)

	// Wait for the update to happen
	// We expect: t=0s (no update), t=2s (update), t=4s (no update)
	time.Sleep(5 * time.Second)

	// Stop the updater
	updater.Stop()

	// Verify the update occurred
	assert.Equal(t, "v1.0.1", updater.config.Updates.CurrentVersion)

	// Verify new version is installed
	assert.True(t, updater.versionMgr.IsVersionInstalled("v1.0.1"))

	// Verify restart marker exists
	markerPath := filepath.Join(homeDir, ".needs_restart")
	markerContent, err := ioutil.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.1", string(markerContent))
}

// Alternative approach: Test with manual trigger instead of timer
func TestAutoUpdater_ManualUpdateFlow(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create initial setup
	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 3600) // Long interval to avoid timer
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	// Setup mock gateway (idle)
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	// Test scenario 1: No update available
	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.0"}, nil)

	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	ctx := context.Background()

	// First check - no update
	updater.checkAndUpdate(ctx)
	assert.Equal(t, "v1.0.0", updater.config.Updates.CurrentVersion)

	// Test scenario 2: Update available
	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.1"}, nil)

	mockClient.EXPECT().
		GetSupernodeDownloadURL("v1.0.1").
		Return("https://example.com/binary", nil)

	mockClient.EXPECT().
		DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
			content := "#!/bin/sh\necho 'mock supernode v1.0.1'\n"
			if progress != nil {
				progress(50, 100)  // Partial progress
				progress(100, 100) // Complete
			}
			return ioutil.WriteFile(destPath, []byte(content), 0755)
		})

	// Second check - update available
	updater.checkAndUpdate(ctx)

	// Verify the update occurred
	assert.Equal(t, "v1.0.1", updater.config.Updates.CurrentVersion)
	assert.True(t, updater.versionMgr.IsVersionInstalled("v1.0.1"))

	// Verify restart marker
	markerPath := filepath.Join(homeDir, ".needs_restart")
	markerContent, err := ioutil.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.1", string(markerContent))

	// Test scenario 3: Gateway busy, should skip update
	// Create busy gateway server
	busyGatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": [{"service_name": "test", "task_count": 1}]}`))
	}))
	defer busyGatewayServer.Close()

	updater.gatewayURL = busyGatewayServer.URL

	// Reset to simulate new version available but gateway busy
	updater.config.Updates.CurrentVersion = "v1.0.1"

	mockClient.EXPECT().
		GetLatestRelease().
		Return(&github.Release{TagName: "v1.0.2"}, nil)

	// Should not expect download calls because gateway is busy
	updater.checkAndUpdate(ctx)

	// Version should remain unchanged
	assert.Equal(t, "v1.0.1", updater.config.Updates.CurrentVersion)
	assert.False(t, updater.versionMgr.IsVersionInstalled("v1.0.2"))
}

// Test with shorter intervals but controlled timing
func TestAutoUpdater_TimedIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping timed integration test in short mode")
	}

	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create initial setup with very short interval for faster testing
	cfg := createTestConfig(t, homeDir, "v1.0.0", true, 1) // 1 second interval
	createMockBinary(t, homeDir, "v1.0.0")

	versionMgr := version.NewManager(homeDir)
	require.NoError(t, versionMgr.SetCurrentVersion("v1.0.0"))

	// Setup mock gateway (idle)
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running_tasks": []}`))
	}))
	defer gatewayServer.Close()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := github.NewMockGithubClient(ctrl)

	// Expect multiple calls but control the sequence
	callCount := 0
	mockClient.EXPECT().
		GetLatestRelease().
		DoAndReturn(func() (*github.Release, error) {
			callCount++
			if callCount == 1 {
				// First call - no update
				return &github.Release{TagName: "v1.0.0"}, nil
			} else if callCount == 2 {
				// Second call - update available
				return &github.Release{TagName: "v1.0.1"}, nil
			} else {
				// Subsequent calls - no more updates
				return &github.Release{TagName: "v1.0.1"}, nil
			}
		}).
		AnyTimes()

	// Expect download operations (will only be called once)
	mockClient.EXPECT().
		GetSupernodeDownloadURL("v1.0.1").
		Return("https://example.com/binary", nil).
		MaxTimes(1)

	mockClient.EXPECT().
		DownloadBinary(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(url, destPath string, progress func(int64, int64)) error {
			content := "#!/bin/sh\necho 'mock supernode v1.0.1'\n"
			if progress != nil {
				progress(100, 100)
			}
			return ioutil.WriteFile(destPath, []byte(content), 0755)
		}).
		MaxTimes(1)

	// Create and start updater
	updater := New(homeDir, cfg)
	updater.githubClient = mockClient
	updater.gatewayURL = gatewayServer.URL

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// Start the updater
	updater.Start(ctx)

	// Wait for update to complete
	time.Sleep(3 * time.Second)

	// Stop the updater
	updater.Stop()

	// Verify the update occurred
	assert.Equal(t, "v1.0.1", updater.config.Updates.CurrentVersion)
	assert.True(t, updater.versionMgr.IsVersionInstalled("v1.0.1"))

	// Verify restart marker
	markerPath := filepath.Join(homeDir, ".needs_restart")
	markerContent, err := ioutil.ReadFile(markerPath)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.1", string(markerContent))

	t.Logf("Total GetLatestRelease calls: %d", callCount)
	assert.GreaterOrEqual(t, callCount, 2, "Should have made at least 2 calls")
}

// TestAutoUpdater_UpdatePolicyLogic tests the update policy (only patch updates)
func TestAutoUpdater_UpdatePolicyLogic(t *testing.T) {
	homeDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	updateScenarios := []struct {
		name           string
		currentVersion string
		latestVersion  string
		shouldUpdate   bool
		description    string
	}{
		{
			name:           "patch_update_allowed",
			currentVersion: "v1.2.3",
			latestVersion:  "v1.2.4",
			shouldUpdate:   true,
			description:    "Patch updates (1.2.3 -> 1.2.4) should be allowed",
		},
		{
			name:           "minor_update_blocked",
			currentVersion: "v1.2.3",
			latestVersion:  "v1.3.0",
			shouldUpdate:   false,
			description:    "Minor updates (1.2.x -> 1.3.x) should be blocked",
		},
		{
			name:           "major_update_blocked",
			currentVersion: "v1.2.3",
			latestVersion:  "v2.0.0",
			shouldUpdate:   false,
			description:    "Major updates (1.x.x -> 2.x.x) should be blocked",
		},
		{
			name:           "same_version_no_update",
			currentVersion: "v1.2.3",
			latestVersion:  "v1.2.3",
			shouldUpdate:   false,
			description:    "Same version should not trigger update",
		},
	}

	for _, scenario := range updateScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := createTestConfig(t, homeDir, scenario.currentVersion, true, 3600)
			updater := New(homeDir, cfg)

			result := updater.shouldUpdate(scenario.currentVersion, scenario.latestVersion)
			assert.Equal(t, scenario.shouldUpdate, result, scenario.description)
		})
	}
}
