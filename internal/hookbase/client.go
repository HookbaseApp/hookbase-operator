// Package hookbase provides a per-namespace SDK client manager for the operator.
package hookbase

import (
	"context"
	"fmt"
	"sync"

	hookbasesdk "github.com/hookbase/hookbase-go"
	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClientManager manages per-namespace Hookbase SDK clients, resolved from K8s Secrets.
type ClientManager struct {
	k8sClient    client.Client
	baseURL      string
	bootstrapKey string // fallback bootstrap key from HookbaseConfig or Helm values

	mu      sync.RWMutex
	clients map[string]*hookbasesdk.Client // key: "namespace/secretName/secretKey"
}

// NewClientManager creates a new ClientManager.
func NewClientManager(k8sClient client.Client, baseURL, bootstrapKey string) *ClientManager {
	return &ClientManager{
		k8sClient:    k8sClient,
		baseURL:      baseURL,
		bootstrapKey: bootstrapKey,
		clients:      make(map[string]*hookbasesdk.Client),
	}
}

// SetBootstrapKey updates the bootstrap API key (used when HookbaseConfig changes).
func (m *ClientManager) SetBootstrapKey(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bootstrapKey = key
}

// GetClient resolves an API key and returns an SDK client.
// Priority: explicit apiKeyRef → default HookbaseAPIKey in namespace → bootstrap key.
func (m *ClientManager) GetClient(ctx context.Context, namespace string, apiKeyRef *hookbasev1alpha1.SecretKeyRef) (*hookbasesdk.Client, error) {
	if apiKeyRef != nil {
		return m.getClientFromSecret(ctx, namespace, apiKeyRef.Name, apiKeyRef.Key)
	}

	// Try to find default HookbaseAPIKey in namespace (labeled hookbase.io/default=true)
	var apiKeyList hookbasev1alpha1.HookbaseAPIKeyList
	if err := m.k8sClient.List(ctx, &apiKeyList, client.InNamespace(namespace), client.MatchingLabels{"hookbase.io/default": "true"}); err == nil && len(apiKeyList.Items) > 0 {
		ref := apiKeyList.Items[0].Spec.SecretRef
		c, err := m.getClientFromSecret(ctx, namespace, ref.Name, ref.Key)
		if err == nil {
			return c, nil
		}
		// Secret not yet available — fall through to bootstrap key
	}

	// Fallback to bootstrap key
	if m.bootstrapKey == "" {
		return nil, fmt.Errorf("no API key available for namespace %s: no apiKeyRef, no default HookbaseAPIKey, and no bootstrap key configured", namespace)
	}

	return m.getOrCreateClient(fmt.Sprintf("bootstrap/%s", m.bootstrapKey[:8]), m.bootstrapKey), nil
}

// getClientFromSecret reads an API key from a K8s Secret and returns an SDK client.
func (m *ClientManager) getClientFromSecret(ctx context.Context, namespace, secretName, secretKey string) (*hookbasesdk.Client, error) {
	if secretKey == "" {
		secretKey = "apiKey"
	}

	var secret corev1.Secret
	if err := m.k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretName}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get Secret %s/%s: %w", namespace, secretName, err)
	}

	apiKey, ok := secret.Data[secretKey]
	if !ok {
		return nil, fmt.Errorf("Secret %s/%s does not contain key %q", namespace, secretName, secretKey)
	}

	cacheKey := fmt.Sprintf("%s/%s/%s", namespace, secretName, secretKey)
	return m.getOrCreateClient(cacheKey, string(apiKey)), nil
}

// getOrCreateClient returns a cached client or creates a new one.
func (m *ClientManager) getOrCreateClient(cacheKey, apiKey string) *hookbasesdk.Client {
	m.mu.RLock()
	if c, ok := m.clients[cacheKey]; ok {
		m.mu.RUnlock()
		return c
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if c, ok := m.clients[cacheKey]; ok {
		return c
	}

	opts := []hookbasesdk.ClientOption{}
	if m.baseURL != "" {
		opts = append(opts, hookbasesdk.WithBaseURL(m.baseURL))
	}
	c := hookbasesdk.New(apiKey, opts...)
	m.clients[cacheKey] = c
	return c
}

// InvalidateCache removes cached clients for a specific secret.
func (m *ClientManager) InvalidateCache(namespace, secretName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := fmt.Sprintf("%s/%s/", namespace, secretName)
	for k := range m.clients {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.clients, k)
		}
	}
}
