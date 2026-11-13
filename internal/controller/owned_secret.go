package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ownedSecretKeySigningSecret is the key an API-generated signing secret is stored under in an
// operator-owned Secret, and the default key read from a WebhookSource's spec.signingSecretRef.
const ownedSecretKeySigningSecret = "signingSecret"

// ownedSecret is the operator-owned Secret "<name>-signing-secret" that holds the credentials the
// Hookbase API returns for one resource (a WebhookSource or a WebhookDestination). The Secret is
// controlled by that resource, so it is garbage-collected with it, and a Secret of the same name
// that the resource does not control is never written to.
type ownedSecret struct {
	c      client.Client
	scheme *runtime.Scheme
	owner  client.Object
	kind   string            // the owner's kind, for error messages
	labels map[string]string // set on the Secret when it is created
}

func (o ownedSecret) name() string {
	return fmt.Sprintf("%s-signing-secret", o.owner.GetName())
}

// read returns the Secret's data. found is false when there is no Secret of that name, and ours is
// false when there is one but the owner does not control it; its data is not returned then.
func (o ownedSecret) read(ctx context.Context) (data map[string][]byte, found, ours bool, err error) {
	existing := &corev1.Secret{}
	err = o.c.Get(ctx, types.NamespacedName{Namespace: o.owner.GetNamespace(), Name: o.name()}, existing)
	if apierrors.IsNotFound(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if !metav1.IsControlledBy(existing, o.owner) {
		return nil, true, false, nil
	}
	return existing.Data, true, true, nil
}

// store sets one key in the Secret, creating the Secret when it is missing. Other keys are kept.
// It refuses, with an error that names the Secret but never the value, to touch a Secret of that
// name that the owner does not control.
func (o ownedSecret) store(ctx context.Context, key, value string) error {
	namespace, name := o.owner.GetNamespace(), o.name()

	existing := &corev1.Secret{}
	err := o.c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, existing)
	if apierrors.IsNotFound(err) {
		labels := map[string]string{"app.kubernetes.io/managed-by": "hookbase-operator"}
		for k, v := range o.labels {
			labels[k] = v
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{key: []byte(value)},
		}
		if err := controllerutil.SetControllerReference(o.owner, secret, o.scheme); err != nil {
			return err
		}
		return o.c.Create(ctx, secret)
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(existing, o.owner) {
		return fmt.Errorf("secret %s/%s exists and is not owned by %s %s; refusing to overwrite it", namespace, name, o.kind, o.owner.GetName())
	}
	if string(existing.Data[key]) == value {
		return nil
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	existing.Data[key] = []byte(value)
	return o.c.Update(ctx, existing)
}
