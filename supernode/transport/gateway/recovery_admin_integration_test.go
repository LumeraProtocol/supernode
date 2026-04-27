package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func setupRecoveryServer(ra *recoveryAdmin, token string) (*httptest.Server, func()) {
	orig := RecoveryAdminToken
	RecoveryAdminToken = token

	mux := http.NewServeMux()
	ra.register(mux)
	ts := httptest.NewServer(mux)

	cleanup := func() {
		ts.Close()
		RecoveryAdminToken = orig
	}
	return ts, cleanup
}

func TestRecoveryAdminIntegration_AuthAndRouting(t *testing.T) {
	ra := &recoveryAdmin{
		enabled:   true,
		reseedSem: make(chan struct{}, 1),
		statusSem: make(chan struct{}, 1),
	}
	ts, cleanup := setupRecoveryServer(ra, "secret")
	defer cleanup()

	t.Run("health unauthorized without header", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/v1/recovery/health")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("health authorized returns 200", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/recovery/health", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("action status route missing action id returns 404", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/recovery/actions//status", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusNotFound)
		}
	})
}

func TestRecoveryAdminIntegration_MethodAndDependencyBehavior(t *testing.T) {
	ra := &recoveryAdmin{
		enabled:   true,
		reseedSem: make(chan struct{}, 1),
		statusSem: make(chan struct{}, 1),
	}
	ts, cleanup := setupRecoveryServer(ra, "secret")
	defer cleanup()

	t.Run("reseed rejects GET", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/recovery/reseed?action_id=a1", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusMethodNotAllowed)
		}
	})

	t.Run("status rejects POST", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/recovery/actions/a1/status", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusMethodNotAllowed)
		}
	})

	t.Run("reseed returns 503 when dependencies missing", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/recovery/reseed?action_id=a1", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusServiceUnavailable)
		}
	})

	t.Run("status returns 503 when dependencies missing", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/recovery/actions/a1/status", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusServiceUnavailable)
		}
	})
}

func TestRecoveryAdminIntegration_SemaphoreThrottling(t *testing.T) {
	ra := &recoveryAdmin{
		enabled:        true,
		cascadeFactory: stubCascadeFactory{},
		p2pClient:      stubP2PClient{},
		reseedSem:      make(chan struct{}, 1),
		statusSem:      make(chan struct{}, 1),
	}
	ts, cleanup := setupRecoveryServer(ra, "secret")
	defer cleanup()

	t.Run("reseed returns 429 while in-progress", func(t *testing.T) {
		ra.reseedSem <- struct{}{}
		defer func() { <-ra.reseedSem }()

		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/recovery/reseed?action_id=a1", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusTooManyRequests)
		}
	})

	t.Run("status returns 429 while in-progress", func(t *testing.T) {
		ra.statusSem <- struct{}{}
		defer func() { <-ra.statusSem }()

		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/recovery/actions/a1/status", nil)
		req.Header.Set(recoveryHeaderToken, "secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status=%d want=%d", resp.StatusCode, http.StatusTooManyRequests)
		}
	})
}
