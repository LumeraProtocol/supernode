package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LumeraProtocol/supernode/v2/p2p"
	cascadeService "github.com/LumeraProtocol/supernode/v2/supernode/cascade"
)

type stubCascadeFactory struct{}

type stubCascadeTask struct{}

type stubP2PClient struct{}

var _ p2p.Client = stubP2PClient{}

func (stubCascadeFactory) NewCascadeRegistrationTask() cascadeService.CascadeTask { return stubCascadeTask{} }

func (stubCascadeTask) Register(context.Context, *cascadeService.RegisterRequest, func(*cascadeService.RegisterResponse) error) error {
	return nil
}

func (stubCascadeTask) Download(context.Context, *cascadeService.DownloadRequest, func(*cascadeService.DownloadResponse) error) error {
	return nil
}

func (stubCascadeTask) CleanupDownload(context.Context, string) error { return nil }

func (stubP2PClient) Retrieve(context.Context, string, ...bool) ([]byte, error) { return nil, nil }
func (stubP2PClient) BatchRetrieve(context.Context, []string, int, string, ...bool) (map[string][]byte, error) {
	return nil, nil
}
func (stubP2PClient) BatchRetrieveStream(context.Context, []string, int32, string, func(string, []byte) error, ...bool) (int32, error) {
	return 0, nil
}
func (stubP2PClient) Store(context.Context, []byte, int) (string, error) { return "", nil }
func (stubP2PClient) StoreBatch(context.Context, [][]byte, int, string) error { return nil }
func (stubP2PClient) Delete(context.Context, string) error { return nil }
func (stubP2PClient) Stats(context.Context) (*p2p.StatsSnapshot, error) { return nil, nil }
func (stubP2PClient) NClosestNodes(context.Context, int, string, ...string) []string { return nil }
func (stubP2PClient) NClosestNodesWithIncludingNodeList(context.Context, int, string, []string, []string) []string {
	return nil
}
func (stubP2PClient) LocalStore(context.Context, string, []byte) (string, error) { return "", nil }
func (stubP2PClient) DisableKey(context.Context, string) error { return nil }
func (stubP2PClient) EnableKey(context.Context, string) error { return nil }
func (stubP2PClient) GetLocalKeys(context.Context, *time.Time, time.Time) ([]string, error) { return nil, nil }

func mustJSONBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func TestRecoveryAdminWrapAuthTokenBehavior(t *testing.T) {
	orig := RecoveryAdminToken
	defer func() { RecoveryAdminToken = orig }()

	ra := &recoveryAdmin{enabled: true}
	hit := false
	h := ra.wrap(func(http.ResponseWriter, *http.Request) { hit = true })

	t.Run("unset token returns 503", func(t *testing.T) {
		RecoveryAdminToken = ""
		hit = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		h(rr, req)

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
		}
		if hit {
			t.Fatal("wrapped handler should not run")
		}
	})

	t.Run("wrong token returns 401", func(t *testing.T) {
		RecoveryAdminToken = "secret"
		hit = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(recoveryHeaderToken, "wrong")

		h(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
		if hit {
			t.Fatal("wrapped handler should not run")
		}
	})

	t.Run("correct token runs handler", func(t *testing.T) {
		RecoveryAdminToken = "secret"
		hit = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(recoveryHeaderToken, "secret")

		h(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if !hit {
			t.Fatal("wrapped handler should run")
		}
	})
}

func TestRecoveryAdminMethodEnforcement(t *testing.T) {
	ra := &recoveryAdmin{}

	t.Run("handleReseed enforces POST", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recovery/reseed?action_id=a1", nil)

		ra.handleReseed(rr, req)

		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("handleStatus enforces GET", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/actions/a1/status", nil)

		ra.handleStatus(rr, req, "a1")

		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
		}
	})
}

func TestRecoveryAdminSemaphoreThrottling(t *testing.T) {
	ra := &recoveryAdmin{
		cascadeFactory: stubCascadeFactory{},
		p2pClient:      stubP2PClient{},
		reseedSem:      make(chan struct{}, 1),
		statusSem:      make(chan struct{}, 1),
	}

	t.Run("reseed returns 429 while another run in progress", func(t *testing.T) {
		ra.reseedSem <- struct{}{}
		defer func() { <-ra.reseedSem }()

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/reseed?action_id=a1", nil)

		ra.handleReseed(rr, req)

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
		}
	})

	t.Run("status returns 429 while another probe in progress", func(t *testing.T) {
		ra.statusSem <- struct{}{}
		defer func() { <-ra.statusSem }()

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recovery/actions/a1/status", nil)

		ra.handleStatus(rr, req, "a1")

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
		}
	})
}

func TestRecoveryAdminBasicResponseShapeAndStatusCodes(t *testing.T) {
	ra := &recoveryAdmin{}

	t.Run("handleStatus missing deps => 503 with ok=false", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recovery/actions/a1/status", nil)

		ra.handleStatus(rr, req, "a1")

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
		}
		body := mustJSONBody(t, rr)
		if body["ok"] != false {
			t.Fatalf("ok = %v, want false", body["ok"])
		}
		if _, ok := body["error"]; !ok {
			t.Fatal("expected error field")
		}
	})

	t.Run("handleReseed missing deps => 503 with ok=false", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/recovery/reseed?action_id=a1", nil)

		ra.handleReseed(rr, req)

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
		}
		body := mustJSONBody(t, rr)
		if body["ok"] != false {
			t.Fatalf("ok = %v, want false", body["ok"])
		}
		if _, ok := body["error"]; !ok {
			t.Fatal("expected error field")
		}
	})
}
