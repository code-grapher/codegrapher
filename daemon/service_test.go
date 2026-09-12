package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:daemon-control-rejects-unauthenticated-callers
func TestControlAPIRequiresTokenAndNonce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &serviceRuntime{
		state:  diskState{Status: Status{Lifecycle: LifecycleReady, Health: Health{Live: true}}},
		nonce:  "expected-nonce",
		token:  "expected-token",
		stopCh: make(chan struct{}),
		cancel: cancel,
	}
	t.Cleanup(cancel)
	handler := runtime.controlHandler()

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/control/v1/status", nil),
		authorizedRequest(http.MethodGet, "/control/v1/status", "wrong", "expected-nonce"),
		authorizedRequest(http.MethodGet, "/control/v1/status", "expected-token", "wrong"),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated response = %d, want %d", response.Code, http.StatusUnauthorized)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(http.MethodGet, "/control/v1/status", "expected-token", "expected-nonce"))
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status response = %d, want %d", response.Code, http.StatusOK)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(http.MethodGet, "/codegrapher/v1/status", "expected-token", "expected-nonce"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("browser API leaked into private lifecycle server: status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, authorizedRequest(http.MethodPost, "/control/v1/stop", "expected-token", "expected-nonce"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("authenticated stop response = %d, want %d", response.Code, http.StatusAccepted)
	}
	select {
	case <-runtime.stopCh:
	default:
		t.Fatal("authenticated stop did not request shutdown")
	}
	if ctx.Err() == nil {
		t.Fatal("authenticated stop did not cancel runtime context")
	}
}

func authorizedRequest(method, path, token, nonce string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-CodeGrapher-Nonce", nonce)
	return request
}
