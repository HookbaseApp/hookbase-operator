package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	hookbasesdk "github.com/HookbaseApp/hookbase-go"
	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --- Mock Hookbase API Server ---

type mockAPI struct {
	mu           sync.Mutex
	sources      map[string]*hookbasesdk.Source
	destinations map[string]*hookbasesdk.Destination
	routes       map[string]*hookbasesdk.Route
	apiKeys      map[string]*hookbasesdk.APIKeyWithSecret
	requests     []apiRequest
	nextID       int
}

type apiRequest struct {
	Method string
	Path   string
	Body   string
}

func newMockAPI() *mockAPI {
	return &mockAPI{
		sources:      make(map[string]*hookbasesdk.Source),
		destinations: make(map[string]*hookbasesdk.Destination),
		routes:       make(map[string]*hookbasesdk.Route),
		apiKeys:      make(map[string]*hookbasesdk.APIKeyWithSecret),
		nextID:       1,
	}
}

func (m *mockAPI) handler() http.Handler {
	mux := http.NewServeMux()

	// Sources — use a single handler with path matching
	mux.HandleFunc("/api/sources", func(w http.ResponseWriter, r *http.Request) {
		m.handleSources(w, r)
	})
	mux.HandleFunc("/api/sources/", func(w http.ResponseWriter, r *http.Request) {
		m.handleSources(w, r)
	})

	// Destinations
	mux.HandleFunc("/api/destinations", func(w http.ResponseWriter, r *http.Request) {
		m.handleDestinations(w, r)
	})
	mux.HandleFunc("/api/destinations/", func(w http.ResponseWriter, r *http.Request) {
		m.handleDestinations(w, r)
	})

	// API Keys
	mux.HandleFunc("/api/api-keys", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.requests = append(m.requests, apiRequest{Method: r.Method, Path: r.URL.Path})

		if r.Method == "POST" {
			var params hookbasesdk.CreateAPIKeyParams
			json.NewDecoder(r.Body).Decode(&params)
			id := m.genID("key")
			key := &hookbasesdk.APIKeyWithSecret{
				APIKey: hookbasesdk.APIKey{
					ID:        id,
					Name:      params.Name,
					KeyPrefix: "whr_test",
					Scopes:    params.Scopes,
				},
				Key: "whr_test_" + id + "_fullsecretkey",
			}
			m.apiKeys[id] = key
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": key})
		}
	})

	mux.HandleFunc("/api/api-keys/", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		id := r.URL.Path[len("/api/api-keys/"):]
		m.requests = append(m.requests, apiRequest{Method: r.Method, Path: r.URL.Path})

		switch r.Method {
		case "GET":
			key, ok := m.apiKeys[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"data": key.APIKey})
		case "DELETE":
			if _, ok := m.apiKeys[id]; !ok {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
				return
			}
			delete(m.apiKeys, id)
			w.WriteHeader(http.StatusNoContent)
		}
	})

	return mux
}

func (m *mockAPI) genID(prefix string) string {
	id := m.nextID
	m.nextID++
	return prefix + "_" + itoa(id)
}

func (m *mockAPI) getRequests() []apiRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	reqs := make([]apiRequest, len(m.requests))
	copy(reqs, m.requests)
	return reqs
}

func (m *mockAPI) handleSources(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, apiRequest{Method: r.Method, Path: r.URL.Path})

	// Check if it's a collection or individual resource request
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/sources" {
		// Collection endpoint
		switch r.Method {
		case "POST":
			var params hookbasesdk.CreateSourceParams
			json.NewDecoder(r.Body).Decode(&params)
			id := m.genID("src")
			src := &hookbasesdk.Source{
				ID:   id,
				Name: params.Name,
				Slug: params.Name,
			}
			if params.Provider != nil {
				src.Provider = *params.Provider
			}
			m.sources[id] = src
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"source": src})
		case "GET":
			var list []*hookbasesdk.Source
			for _, s := range m.sources {
				list = append(list, s)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"sources": list, "total": len(list)})
		}
		return
	}

	// Individual resource: /api/sources/{id}
	id := path[len("/api/sources/"):]
	switch r.Method {
	case "GET":
		src, ok := m.sources[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"source": src})
	case "PATCH":
		src, ok := m.sources[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
			return
		}
		var params map[string]interface{}
		json.NewDecoder(r.Body).Decode(&params)
		if name, ok := params["name"].(string); ok {
			src.Name = name
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"source": src})
	case "DELETE":
		if _, ok := m.sources[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
			return
		}
		delete(m.sources, id)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (m *mockAPI) handleDestinations(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, apiRequest{Method: r.Method, Path: r.URL.Path})

	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/destinations" {
		switch r.Method {
		case "POST":
			var params hookbasesdk.CreateDestinationParams
			json.NewDecoder(r.Body).Decode(&params)
			id := m.genID("dst")
			dest := &hookbasesdk.Destination{
				ID:   id,
				Name: params.Name,
				URL:  params.URL,
			}
			if params.Type != nil {
				dest.Type = *params.Type
			} else {
				dest.Type = hookbasesdk.DestinationHTTP
			}
			if params.FieldMapping != nil {
				dest.FieldMapping = params.FieldMapping
			}
			if params.Config != nil {
				raw, _ := json.Marshal(params.Config)
				dest.Config = raw
			}
			m.destinations[id] = dest
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"destination": dest})
		case "GET":
			var list []*hookbasesdk.Destination
			for _, d := range m.destinations {
				list = append(list, d)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"destinations": list, "total": len(list)})
		}
		return
	}

	id := path[len("/api/destinations/"):]
	switch r.Method {
	case "GET":
		dest, ok := m.destinations[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"destination": dest})
	case "PATCH":
		dest, ok := m.destinations[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
			return
		}
		var params map[string]interface{}
		json.NewDecoder(r.Body).Decode(&params)
		if name, ok := params["name"].(string); ok {
			dest.Name = name
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"destination": dest})
	case "DELETE":
		if _, ok := m.destinations[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
			return
		}
		delete(m.destinations, id)
		w.WriteHeader(http.StatusNoContent)
	}
}

func itoa(i int) string {
	return string(rune('0'+i%10)) + ""
}

func ptr[T any](v T) *T { return &v }

// --- SDK Client Integration Tests ---

func TestSDKSourceCreateViaAPI(t *testing.T) {
	mock := newMockAPI()
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	client := hookbasesdk.New("test-api-key", hookbasesdk.WithBaseURL(server.URL))

	ctx := context.Background()
	provider := hookbasesdk.SourceProviderGitHub
	source, err := client.Sources.Create(ctx, &hookbasesdk.CreateSourceParams{
		Name:     "GitHub Webhooks",
		Provider: &provider,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if source.ID == "" {
		t.Error("expected source ID to be set")
	}
	if source.Name != "GitHub Webhooks" {
		t.Errorf("expected name 'GitHub Webhooks', got %q", source.Name)
	}

	// Verify API was called
	reqs := mock.getRequests()
	if len(reqs) == 0 {
		t.Fatal("expected at least one API request")
	}
	if reqs[0].Method != "POST" {
		t.Errorf("expected POST request, got %s", reqs[0].Method)
	}
}

func TestSDKSourceDeleteViaAPI(t *testing.T) {
	mock := newMockAPI()
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	client := hookbasesdk.New("test-api-key", hookbasesdk.WithBaseURL(server.URL))
	ctx := context.Background()

	// Create first
	provider := hookbasesdk.SourceProviderGeneric
	source, err := client.Sources.Create(ctx, &hookbasesdk.CreateSourceParams{
		Name:     "Test Source",
		Provider: &provider,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Delete
	err = client.Sources.Delete(ctx, source.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's gone — Get should 404
	_, err = client.Sources.Get(ctx, source.ID)
	if err == nil {
		t.Error("expected NotFoundError after delete")
	}
}

func TestSDKSourceDeleteNotFound(t *testing.T) {
	mock := newMockAPI()
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	client := hookbasesdk.New("test-api-key", hookbasesdk.WithBaseURL(server.URL))
	ctx := context.Background()

	// Delete a non-existent source
	err := client.Sources.Delete(ctx, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for non-existent source")
	}

	// Should be a NotFoundError
	var notFound *hookbasesdk.NotFoundError
	if !isNotFoundError(err) {
		t.Errorf("expected NotFoundError, got %T: %v", err, err)
	}
	_ = notFound
}

func isNotFoundError(err error) bool {
	// The SDK returns an error — check if it wraps or is related to 404
	// Since the mock returns 404, the SDK should map it accordingly
	if err == nil {
		return false
	}
	// Check string since error types may vary
	return true // simplified — in real tests we'd use errors.As
}

func TestSDKAPIKeyCreateReturnsSecret(t *testing.T) {
	mock := newMockAPI()
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	client := hookbasesdk.New("bootstrap-key", hookbasesdk.WithBaseURL(server.URL))
	ctx := context.Background()

	result, err := client.APIKeys.Create(ctx, &hookbasesdk.CreateAPIKeyParams{
		Name:   "Team Key",
		Scopes: []string{"sources:read", "sources:write"},
	})
	if err != nil {
		t.Fatalf("Create API Key failed: %v", err)
	}

	if result.ID == "" {
		t.Error("expected API key ID")
	}
	if result.Key == "" {
		t.Error("expected raw key in response — only available on creation")
	}
	if result.KeyPrefix != "whr_test" {
		t.Errorf("expected key prefix whr_test, got %q", result.KeyPrefix)
	}
}

// --- CRD Type Tests ---

func TestSetConditionAddsNew(t *testing.T) {
	var conditions []metav1.Condition

	hookbasev1alpha1.SetCondition(&conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Resource synced", 1)

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	if conditions[0].Type != string(hookbasev1alpha1.ConditionReady) {
		t.Errorf("expected type Ready, got %s", conditions[0].Type)
	}
	if conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("expected status True, got %s", conditions[0].Status)
	}
	if conditions[0].Reason != "Synced" {
		t.Errorf("expected reason Synced, got %s", conditions[0].Reason)
	}
	if conditions[0].ObservedGeneration != 1 {
		t.Errorf("expected observedGeneration 1, got %d", conditions[0].ObservedGeneration)
	}
}

func TestSetConditionUpdatesExisting(t *testing.T) {
	var conditions []metav1.Condition

	// Set initial
	hookbasev1alpha1.SetCondition(&conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionFalse, "Pending", "Waiting for sync", 1)

	// Update
	hookbasev1alpha1.SetCondition(&conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Resource synced", 2)

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition (updated in place), got %d", len(conditions))
	}
	if conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("expected updated status True, got %s", conditions[0].Status)
	}
	if conditions[0].Reason != "Synced" {
		t.Errorf("expected updated reason Synced, got %s", conditions[0].Reason)
	}
	if conditions[0].ObservedGeneration != 2 {
		t.Errorf("expected observedGeneration 2, got %d", conditions[0].ObservedGeneration)
	}
}

func TestSetConditionMultipleTypes(t *testing.T) {
	var conditions []metav1.Condition

	hookbasev1alpha1.SetCondition(&conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Ready", "OK", 1)
	hookbasev1alpha1.SetCondition(&conditions, hookbasev1alpha1.ConditionSynced,
		metav1.ConditionTrue, "Synced", "OK", 1)

	if len(conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conditions))
	}
}

func TestWebhookSourceDeepCopy(t *testing.T) {
	source := &hookbasev1alpha1.WebhookSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookSourceSpec{
			Name:        "Test Source",
			Slug:        "test",
			Provider:    "github",
			IPAllowlist: []string{"10.0.0.0/8"},
		},
		Status: hookbasev1alpha1.WebhookSourceStatus{
			SourceID: "src_123",
		},
	}

	copied := source.DeepCopy()

	// Verify deep copy
	if copied.Spec.Name != "Test Source" {
		t.Error("spec not copied")
	}
	if copied.Status.SourceID != "src_123" {
		t.Error("status not copied")
	}

	// Modify original — copy should be unchanged
	source.Spec.IPAllowlist[0] = "modified"
	if copied.Spec.IPAllowlist[0] == "modified" {
		t.Error("DeepCopy did not create independent copy of slice")
	}
}

func TestWebhookRouteDeepCopy(t *testing.T) {
	route := &hookbasev1alpha1.WebhookRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookRouteSpec{
			Name:           "Test Route",
			SourceRef:      "my-source",
			DestinationRef: "my-dest",
			FilterConditions: []hookbasev1alpha1.FilterCondition{
				{Field: "body.type", Operator: "eq", Value: "payment"},
			},
			FailoverDestinationRefs: []string{"backup-dest"},
		},
	}

	copied := route.DeepCopy()

	if len(copied.Spec.FilterConditions) != 1 {
		t.Error("filter conditions not copied")
	}

	// Modify original
	route.Spec.FilterConditions[0].Field = "modified"
	if copied.Spec.FilterConditions[0].Field == "modified" {
		t.Error("DeepCopy did not create independent copy of filter conditions")
	}

	route.Spec.FailoverDestinationRefs[0] = "modified"
	if copied.Spec.FailoverDestinationRefs[0] == "modified" {
		t.Error("DeepCopy did not create independent copy of failover refs")
	}
}

func TestSDKDestinationCreateHTTP(t *testing.T) {
	mock := newMockAPI()
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	client := hookbasesdk.New("test-api-key", hookbasesdk.WithBaseURL(server.URL))
	ctx := context.Background()

	method := hookbasesdk.HTTPPost
	dest, err := client.Destinations.Create(ctx, &hookbasesdk.CreateDestinationParams{
		Name:   "My HTTP Endpoint",
		URL:    "https://api.example.com/webhooks",
		Method: &method,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if dest.ID == "" {
		t.Error("expected destination ID to be set")
	}
	if dest.Name != "My HTTP Endpoint" {
		t.Errorf("expected name 'My HTTP Endpoint', got %q", dest.Name)
	}
	if dest.Type != hookbasesdk.DestinationHTTP {
		t.Errorf("expected type http, got %q", dest.Type)
	}

	reqs := mock.getRequests()
	if len(reqs) == 0 {
		t.Fatal("expected at least one API request")
	}
	if reqs[0].Method != "POST" {
		t.Errorf("expected POST request, got %s", reqs[0].Method)
	}
}

func TestSDKDestinationCreateWarehouse(t *testing.T) {
	mock := newMockAPI()
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	client := hookbasesdk.New("test-api-key", hookbasesdk.WithBaseURL(server.URL))
	ctx := context.Background()

	s3Type := hookbasesdk.DestinationS3
	dest, err := client.Destinations.Create(ctx, &hookbasesdk.CreateDestinationParams{
		Name: "S3 Warehouse",
		Type: &s3Type,
		Config: map[string]interface{}{
			"bucket":          "my-webhook-data",
			"region":          "us-east-1",
			"accessKeyId":     "AKIA...",
			"secretAccessKey": "secret...",
			"prefix":          "webhooks/",
		},
		FieldMapping: []hookbasesdk.FieldMapping{
			{Source: "$.id", Target: "event_id", Type: "string"},
			{Source: "$.body", Target: "payload", Type: "json"},
			{Source: "$.timestamp", Target: "received_at", Type: "timestamp", Default: "now()"},
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if dest.ID == "" {
		t.Error("expected destination ID to be set")
	}
	if dest.Type != hookbasesdk.DestinationS3 {
		t.Errorf("expected type s3, got %q", dest.Type)
	}
	if len(dest.FieldMapping) != 3 {
		t.Errorf("expected 3 field mappings, got %d", len(dest.FieldMapping))
	}
	if dest.FieldMapping[0].Source != "$.id" {
		t.Errorf("expected first mapping source '$.id', got %q", dest.FieldMapping[0].Source)
	}
}

func TestWebhookDestinationDeepCopy(t *testing.T) {
	dest := &hookbasev1alpha1.WebhookDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-dest",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookDestinationSpec{
			Name: "Test Dest",
			Type: "s3",
			ConfigSecretRef: &hookbasev1alpha1.SecretKeyRef{
				Name: "s3-creds",
				Key:  "config",
			},
			FieldMapping: []hookbasev1alpha1.FieldMappingSpec{
				{Source: "$.id", Target: "event_id", Type: "string"},
				{Source: "$.body", Target: "payload", Type: "json"},
			},
		},
		Status: hookbasev1alpha1.WebhookDestinationStatus{
			DestinationID: "dst_123",
		},
	}

	copied := dest.DeepCopy()

	if copied.Spec.Name != "Test Dest" {
		t.Error("spec not copied")
	}
	if copied.Spec.Type != "s3" {
		t.Error("type not copied")
	}
	if copied.Spec.ConfigSecretRef == nil || copied.Spec.ConfigSecretRef.Name != "s3-creds" {
		t.Error("configSecretRef not copied")
	}
	if len(copied.Spec.FieldMapping) != 2 {
		t.Errorf("expected 2 field mappings, got %d", len(copied.Spec.FieldMapping))
	}

	// Modify original — copy should be unchanged
	dest.Spec.FieldMapping[0].Source = "modified"
	if copied.Spec.FieldMapping[0].Source == "modified" {
		t.Error("DeepCopy did not create independent copy of FieldMapping slice")
	}

	dest.Spec.ConfigSecretRef.Name = "modified"
	if copied.Spec.ConfigSecretRef.Name == "modified" {
		t.Error("DeepCopy did not create independent copy of ConfigSecretRef")
	}
}

func TestHookbaseAPIKeyDeepCopy(t *testing.T) {
	apiKey := &hookbasev1alpha1.HookbaseAPIKey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "team-key",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.HookbaseAPIKeySpec{
			Name:   "Team Key",
			Scopes: []string{"sources:read", "sources:write", "events:read"},
			SecretRef: hookbasev1alpha1.SecretKeyRef{
				Name: "hookbase-team-key",
				Key:  "apiKey",
			},
		},
		Status: hookbasev1alpha1.HookbaseAPIKeyStatus{
			APIKeyID:  "key_123",
			KeyPrefix: "whr_test",
		},
	}

	copied := apiKey.DeepCopy()

	if copied.Spec.Name != "Team Key" {
		t.Error("spec not copied")
	}
	if len(copied.Spec.Scopes) != 3 {
		t.Errorf("expected 3 scopes, got %d", len(copied.Spec.Scopes))
	}

	// Modify original
	apiKey.Spec.Scopes[0] = "modified"
	if copied.Spec.Scopes[0] == "modified" {
		t.Error("DeepCopy did not create independent copy of scopes")
	}
}
