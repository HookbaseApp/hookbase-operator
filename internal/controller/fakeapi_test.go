package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// fakeAPI is a stand-in for the Hookbase API. It records every request body and answers each
// request with what handle returns.
type fakeAPI struct {
	mu       sync.Mutex
	requests []recorded
	handle   func(method, path string) (status int, body interface{})
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	a.requests = append(a.requests, recorded{Method: r.Method, Path: r.URL.Path, Body: body})
	status, resp := a.handle(r.Method, r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// find returns the requests with this method whose path ends in suffix.
func (a *fakeAPI) find(method, suffix string) []recorded {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []recorded
	for _, r := range a.requests {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			out = append(out, r)
		}
	}
	return out
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := hookbasev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}
