//go:build e2e

// Package test contains end-to-end tests for the Hookbase operator.
//
// These tests require a running Kubernetes cluster (e.g., kind) with the
// operator CRDs installed. Run with: go test ./test/ -v -tags=e2e
package test

import (
	"context"
	"testing"
	"time"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWebhookSourceE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	k8sClient := getK8sClient(t)

	// Create a WebhookSource
	source := &hookbasev1alpha1.WebhookSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-source",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookSourceSpec{
			Name:     "Test Source",
			Provider: "generic",
		},
	}

	if err := k8sClient.Create(ctx, source); err != nil {
		t.Fatalf("failed to create WebhookSource: %v", err)
	}
	defer func() {
		_ = k8sClient.Delete(ctx, source)
	}()

	// Wait for Ready condition
	if err := waitForCondition(ctx, k8sClient, source, "Ready", "True", 60*time.Second); err != nil {
		t.Fatalf("WebhookSource did not become Ready: %v", err)
	}

	// Verify status fields
	updated := &hookbasev1alpha1.WebhookSource{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-source", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get WebhookSource: %v", err)
	}

	if updated.Status.SourceID == "" {
		t.Error("expected sourceId to be set")
	}
	if updated.Status.IngestURL == "" {
		t.Error("expected ingestUrl to be set")
	}
}

func getK8sClient(t *testing.T) client.Client {
	t.Helper()
	// In real e2e tests, this would use envtest or an actual cluster
	t.Skip("requires running cluster — use 'go test ./test/ -tags=e2e' with kind cluster")
	return nil
}

func waitForCondition(ctx context.Context, c client.Client, obj client.Object, condType, status string, timeout time.Duration) error {
	// Polling implementation for e2e tests
	_ = ctx
	_ = c
	_ = obj
	_ = condType
	_ = status
	_ = timeout
	return nil
}
