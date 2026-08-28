// Copyright © 2025 Alibaba Cloud. All rights reserved.

package externalsecret

import (
	"context"
	"fmt"
	"strings"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SimpleSecretOperationHandler handles the Secret write flow for an ExternalSecret
type SimpleSecretOperationHandler struct {
	Client                 client.Client
	CleanUpSecretOnFailure bool
	Log                    logr.Logger
}

// NewSimpleSecretOperationHandler creates a new Secret operation handler
func NewSimpleSecretOperationHandler(client client.Client, cleanUpSecretOnFailure bool, log logr.Logger) *SimpleSecretOperationHandler {
	return &SimpleSecretOperationHandler{
		Client:                 client,
		CleanUpSecretOnFailure: cleanUpSecretOnFailure,
		Log:                    log,
	}
}

// HandleSecretOperation runs the complete Secret operation flow
func (h *SimpleSecretOperationHandler) HandleSecretOperation(
	ctx context.Context,
	externalSec *api.ExternalSecret,
	secretData map[string][]byte,
	currentData map[string][]byte,
	metadataTargets map[string]map[string]string, // TemplateFrom metadata targets
) error {
	secretName := externalSec.Name
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Name != "" {
		secretName = externalSec.Spec.Target.Name
	}

	// CleanUpSecretOnFailure: an empty dataset means total provider failure
	if h.CleanUpSecretOnFailure && len(secretData) == 0 {
		return h.handleProviderDeletion(ctx, externalSec, secretName)
	}

	currentSecret := &corev1.Secret{}
	err := h.Client.Get(ctx, types.NamespacedName{
		Namespace: externalSec.Namespace,
		Name:      secretName,
	}, currentSecret)

	secretExists := err == nil
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get current secret: %w", err)
	}

	labels := make(map[string]string)
	annotations := make(map[string]string)

	if secretExists {
		for k, v := range currentSecret.Labels {
			labels[k] = v
		}
		for k, v := range currentSecret.Annotations {
			annotations[k] = v
		}
	}

	// Overlay TemplateFrom-processed metadata
	if metadataTargets != nil {
		if annTargets, exists := metadataTargets["annotations"]; exists {
			for k, v := range annTargets {
				annotations[k] = v
			}
		}
		if labelTargets, exists := metadataTargets["labels"]; exists {
			for k, v := range labelTargets {
				labels[k] = v
			}
		}
	}

	// Create or update secret WITHOUT owner reference
	if secretExists {
		return h.updateSecretWithoutOwner(ctx, currentSecret, secretData, labels, annotations)
	} else {
		return h.createSecretWithoutOwner(ctx, externalSec, secretName, secretData, labels, annotations)
	}
}

// createSecretWithoutOwner creates a new secret without owner reference (original behavior)
func (h *SimpleSecretOperationHandler) createSecretWithoutOwner(
	ctx context.Context,
	externalSec *api.ExternalSecret,
	secretName string,
	secretData map[string][]byte,
	labels, annotations map[string]string,
) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   externalSec.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Data: secretData,
		Type: h.getSecretType(externalSec),
	}

	// Do NOT set owner reference
	err := h.Client.Create(ctx, secret)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Secret appeared after the existence check: re-fetch and update
			// (optimistic lock); on cache-lag NotFound the backoff converges.
			existing := &corev1.Secret{}
			if getErr := h.Client.Get(ctx, types.NamespacedName{
				Namespace: externalSec.Namespace,
				Name:      secretName,
			}, existing); getErr != nil {
				return fmt.Errorf("secret already exists but failed to re-fetch it: %w", getErr)
			}
			return h.updateSecretWithoutOwner(ctx, existing, secretData, labels, annotations)
		}
		if strings.Contains(err.Error(), "unable to create new content in namespace") &&
			strings.Contains(err.Error(), "because it is being terminated") {
			h.Log.Info("Skipping secret creation as namespace is terminating",
				"namespace", externalSec.Namespace, "secret", secretName)
			return nil // Gracefully handle namespace termination
		}
		return fmt.Errorf("failed to create secret without owner reference: %w", err)
	}

	h.Log.Info("Created secret without owner reference", "namespace", externalSec.Namespace, "name", secretName)
	return nil
}

// updateSecretWithoutOwner updates an existing secret without owner reference
func (h *SimpleSecretOperationHandler) updateSecretWithoutOwner(
	ctx context.Context,
	currentSecret *corev1.Secret,
	secretData map[string][]byte,
	labels, annotations map[string]string,
) error {
	// DeepCopy avoids modifying the cached object (prevents data races)
	updatedSecret := currentSecret.DeepCopy()

	updatedSecret.Data = secretData
	updatedSecret.Labels = labels
	updatedSecret.Annotations = annotations

	// Do NOT modify owner references (preserve any existing ones)
	if err := h.Client.Update(ctx, updatedSecret); err != nil {
		return fmt.Errorf("failed to update secret without owner reference: %w", err)
	}

	h.Log.Info("Updated secret without owner reference",
		"namespace", currentSecret.Namespace, "name", currentSecret.Name)
	return nil
}

// handleProviderDeletion deletes the target Secret on the total-failure path
// with CleanUpSecretOnFailure=true -- NOT when the ExternalSecret itself is
// being deleted, and not from any other round type.
func (h *SimpleSecretOperationHandler) handleProviderDeletion(
	ctx context.Context,
	externalSec *api.ExternalSecret,
	secretName string,
) error {
	// Fetch first so a missing Secret is a no-op instead of a delete error.
	secret := &corev1.Secret{}
	err := h.Client.Get(ctx, types.NamespacedName{
		Namespace: externalSec.Namespace,
		Name:      secretName,
	}, secret)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get secret for deletion: %w", err)
	}

	if err := h.Client.Delete(ctx, secret); err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	h.Log.Info("Deleted secret due to provider data unavailability",
		"namespace", externalSec.Namespace, "name", secretName)
	return nil
}

// getSecretType returns the Secret type from spec, defaulting to Opaque
func (h *SimpleSecretOperationHandler) getSecretType(externalSec *api.ExternalSecret) corev1.SecretType {
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Template != nil && externalSec.Spec.Target.Template.Type != "" {
		return externalSec.Spec.Target.Template.Type
	}
	if externalSec.Spec.Type != "" {
		return corev1.SecretType(externalSec.Spec.Type)
	}
	return corev1.SecretTypeOpaque
}
