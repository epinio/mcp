package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// AdoptionFieldManager is the server-side-apply fieldManager used by the
// adoption helpers. Distinct from Epinio's own fieldManager so ownership
// conflicts between the MCP and Epinio's REST writes are explicit.
const AdoptionFieldManager = "epinio-mcp-adoption"

// EpinioAdoptedAnnotation marks an App CRD (and workload) as adopted by a
// non-Epinio actor (kubectl install, adopt_app tool). MCP tooling refuses
// destructive write ops on apps carrying this annotation.
const EpinioAdoptedAnnotation = "epinio.io/adopted"

// CanonicalLabels returns the four app.kubernetes.io/* labels Epinio's
// label-based workload discovery (internal/application/workload.go) uses
// to connect a Deployment/Service back to an App CRD.
func CanonicalLabels(appName, appNamespace string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       appName,
		"app.kubernetes.io/part-of":    appNamespace,
		"app.kubernetes.io/managed-by": "epinio",
		"app.kubernetes.io/component":  "application",
	}
}

// KubeClient wraps the typed + dynamic in-cluster Kubernetes clients we need
// for Epinio capability checks (app CRD reads, SelfSubjectAccessReviews).
type KubeClient struct {
	Typed   kubernetes.Interface
	Dynamic dynamic.Interface
}

var (
	kubeOnce   sync.Once
	kubeClient *KubeClient
	kubeErr    error
)

// GetKubeClient returns a lazily initialized in-cluster Kubernetes client.
// When the binary runs outside a cluster (local dev), rest.InClusterConfig
// returns an error and subsequent calls return the same error — callers
// should surface this as "not available" rather than panic.
func GetKubeClient() (*KubeClient, error) {
	kubeOnce.Do(func() {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			kubeErr = fmt.Errorf("in-cluster kubeconfig: %w", err)
			return
		}
		typed, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			kubeErr = fmt.Errorf("typed kube client: %w", err)
			return
		}
		dyn, err := dynamic.NewForConfig(cfg)
		if err != nil {
			kubeErr = fmt.Errorf("dynamic kube client: %w", err)
			return
		}
		kubeClient = &KubeClient{Typed: typed, Dynamic: dyn}
	})
	return kubeClient, kubeErr
}

// CanI performs a SelfSubjectAccessReview against the pod's ServiceAccount.
// The namespace is optional — pass "" for cluster-scope resources.
func (k *KubeClient) CanI(ctx context.Context, verb, group, resource, namespace string) (bool, error) {
	review := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     group,
				Resource:  resource,
			},
		},
	}
	resp, err := k.Typed.AuthorizationV1().
		SelfSubjectAccessReviews().
		Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("SSAR: %w", err)
	}
	return resp.Status.Allowed, nil
}

// EpinioAppGVR is the GroupVersionResource for Epinio's Application CRD.
var EpinioAppGVR = schema.GroupVersionResource{
	Group:    "application.epinio.io",
	Version:  "v1",
	Resource: "apps",
}

// ReadEpinioApp fetches the Epinio Application CRD for the given namespace/name.
// Returns the raw unstructured object so callers can pull specific spec fields
// (e.g. blobuid) without binding to an Epinio-typed dependency.
func (k *KubeClient) ReadEpinioApp(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	return k.Dynamic.Resource(EpinioAppGVR).
		Namespace(namespace).
		Get(ctx, name, metav1.GetOptions{})
}

// AppBlobUID reads the .spec.blobuid of the Epinio Application CRD. Returns
// the uid string or an error if missing. This is the key we use to fetch the
// staged source tarball from S3.
func (k *KubeClient) AppBlobUID(ctx context.Context, namespace, name string) (string, error) {
	app, err := k.ReadEpinioApp(ctx, namespace, name)
	if err != nil {
		return "", fmt.Errorf("read app %q in %q: %w", name, namespace, err)
	}
	uid, found, err := unstructured.NestedString(app.Object, "spec", "blobuid")
	if err != nil {
		return "", fmt.Errorf("extract blobuid: %w", err)
	}
	if !found || uid == "" {
		return "", fmt.Errorf("app %q in %q has no blobuid", name, namespace)
	}
	return uid, nil
}

// IsAppAdopted returns true when the App CRD carries the
// epinio.io/adopted annotation set to "true".
func (k *KubeClient) IsAppAdopted(ctx context.Context, namespace, name string) (bool, error) {
	app, err := k.ReadEpinioApp(ctx, namespace, name)
	if err != nil {
		return false, err
	}
	annotations := app.GetAnnotations()
	return annotations[EpinioAdoptedAnnotation] == "true", nil
}

// WriteEpinioApp creates or updates an App CRD via server-side apply.
// The object is expected to carry apiVersion/kind/metadata/spec as usual.
// Uses the AdoptionFieldManager so writes from the MCP don't fight Epinio's
// own REST-driven writes for ownership.
func (k *KubeClient) WriteEpinioApp(ctx context.Context, obj *unstructured.Unstructured) error {
	if obj.GetAPIVersion() == "" {
		obj.SetAPIVersion("application.epinio.io/v1")
	}
	if obj.GetKind() == "" {
		obj.SetKind("App")
	}
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return fmt.Errorf("marshal app: %w", err)
	}
	_, err = k.Dynamic.Resource(EpinioAppGVR).
		Namespace(obj.GetNamespace()).
		Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
			FieldManager: AdoptionFieldManager,
			Force:        ptr(true),
		})
	if err != nil {
		return fmt.Errorf("apply app %q in %q: %w", obj.GetName(), obj.GetNamespace(), err)
	}
	return nil
}

// DeleteEpinioApp removes an App CRD. Returns nil on NotFound.
func (k *KubeClient) DeleteEpinioApp(ctx context.Context, namespace, name string) error {
	err := k.Dynamic.Resource(EpinioAppGVR).
		Namespace(namespace).
		Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete app %q in %q: %w", name, namespace, err)
	}
	return nil
}

// GetDeployment fetches a Deployment via the typed client.
func (k *KubeClient) GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error) {
	return k.Typed.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
}

// GetService fetches a Service via the typed client.
func (k *KubeClient) GetService(ctx context.Context, namespace, name string) (*corev1.Service, error) {
	return k.Typed.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
}

// ListIngresses lists Ingresses in a namespace. Used by reconcile_app to
// derive routes from Ingress hosts targeting the app's Service.
func (k *KubeClient) ListIngresses(ctx context.Context, namespace string) ([]networkingv1.Ingress, error) {
	list, err := k.Typed.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list ingresses in %q: %w", namespace, err)
	}
	return list.Items, nil
}

// metadataPatch is a minimal JSON patch payload covering labels/annotations.
// Used by LabelDeployment/LabelService to apply canonical Epinio labels
// without overwriting other metadata.
type metadataPatch struct {
	Metadata metadataPatchMeta `json:"metadata"`
}

type metadataPatchMeta struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// labelPatchPayload builds a strategic-merge-patch that sets the given
// labels/annotations on an object's metadata. nil maps mean "don't touch."
func labelPatchPayload(labels, annotations map[string]string) ([]byte, error) {
	patch := metadataPatch{
		Metadata: metadataPatchMeta{
			Labels:      labels,
			Annotations: annotations,
		},
	}
	return json.Marshal(patch)
}

// LabelDeployment applies labels + annotations to a Deployment's
// metadata via strategic merge patch. Pass nil for a map to leave that
// dimension untouched.
func (k *KubeClient) LabelDeployment(ctx context.Context, namespace, name string, labels, annotations map[string]string) error {
	patch, err := labelPatchPayload(labels, annotations)
	if err != nil {
		return fmt.Errorf("build deployment patch: %w", err)
	}
	_, err = k.Typed.AppsV1().Deployments(namespace).
		Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{
			FieldManager: AdoptionFieldManager,
		})
	if err != nil {
		return fmt.Errorf("patch deployment %q in %q: %w", name, namespace, err)
	}
	return nil
}

// LabelDeploymentPodTemplate applies labels + annotations to the pod
// template (spec.template.metadata) of a Deployment. Epinio's pod-level
// discovery (`epinio.io/app-container`, etc.) requires labels on the
// pod template, not just the Deployment itself.
func (k *KubeClient) LabelDeploymentPodTemplate(ctx context.Context, namespace, name string, labels, annotations map[string]string) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": metadataPatchMeta{
					Labels:      labels,
					Annotations: annotations,
				},
			},
		},
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("build pod-template patch: %w", err)
	}
	_, err = k.Typed.AppsV1().Deployments(namespace).
		Patch(ctx, name, types.StrategicMergePatchType, data, metav1.PatchOptions{
			FieldManager: AdoptionFieldManager,
		})
	if err != nil {
		return fmt.Errorf("patch deployment pod template %q in %q: %w", name, namespace, err)
	}
	return nil
}

// LabelService applies labels + annotations to a Service's metadata.
func (k *KubeClient) LabelService(ctx context.Context, namespace, name string, labels, annotations map[string]string) error {
	patch, err := labelPatchPayload(labels, annotations)
	if err != nil {
		return fmt.Errorf("build service patch: %w", err)
	}
	_, err = k.Typed.CoreV1().Services(namespace).
		Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{
			FieldManager: AdoptionFieldManager,
		})
	if err != nil {
		return fmt.Errorf("patch service %q in %q: %w", name, namespace, err)
	}
	return nil
}

// GetSecret fetches a Secret via the typed client.
func (k *KubeClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	return k.Typed.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

// WriteSecret creates or updates a Secret. Existing keys not in `data` are
// preserved (merge semantics); to remove a key, update with an empty value
// and handle removal explicitly.
func (k *KubeClient) WriteSecret(ctx context.Context, namespace, name string, data map[string][]byte, labels, annotations map[string]string) error {
	secrets := k.Typed.CoreV1().Secrets(namespace)
	existing, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get secret %q in %q: %w", name, namespace, err)
	}
	if apierrors.IsNotFound(err) {
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   namespace,
				Labels:      labels,
				Annotations: annotations,
			},
			Type: corev1.SecretTypeOpaque,
			Data: data,
		}
		if _, err := secrets.Create(ctx, sec, metav1.CreateOptions{
			FieldManager: AdoptionFieldManager,
		}); err != nil {
			return fmt.Errorf("create secret %q in %q: %w", name, namespace, err)
		}
		return nil
	}
	// Merge into existing data.
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	for k2, v := range data {
		existing.Data[k2] = v
	}
	if len(labels) > 0 {
		if existing.Labels == nil {
			existing.Labels = map[string]string{}
		}
		for k2, v := range labels {
			existing.Labels[k2] = v
		}
	}
	if len(annotations) > 0 {
		if existing.Annotations == nil {
			existing.Annotations = map[string]string{}
		}
		for k2, v := range annotations {
			existing.Annotations[k2] = v
		}
	}
	if _, err := secrets.Update(ctx, existing, metav1.UpdateOptions{
		FieldManager: AdoptionFieldManager,
	}); err != nil {
		return fmt.Errorf("update secret %q in %q: %w", name, namespace, err)
	}
	return nil
}

// RestartDeployment triggers a rolling restart of a Deployment by patching
// its pod-template `kubectl.kubernetes.io/restartedAt` annotation — the same
// mechanism `kubectl rollout restart` uses. The value is the current RFC3339
// timestamp so each invocation produces a distinct patch and guarantees a
// new rollout.
func (k *KubeClient) RestartDeployment(ctx context.Context, namespace, name string) error {
	annotations := map[string]string{
		"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339),
	}
	return k.LabelDeploymentPodTemplate(ctx, namespace, name, nil, annotations)
}

// RemoveLabels removes the given label keys from a Deployment's metadata
// (not pod template) and strips matching annotations. Uses a JSON Merge
// Patch (RFC 7396) where null values remove keys.
func (k *KubeClient) RemoveDeploymentMetadata(ctx context.Context, namespace, name string, labelKeys, annotationKeys []string) error {
	patch := buildRemovePatch(labelKeys, annotationKeys)
	if patch == nil {
		return nil
	}
	_, err := k.Typed.AppsV1().Deployments(namespace).
		Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{
			FieldManager: AdoptionFieldManager,
		})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("strip metadata on deployment %q/%q: %w", namespace, name, err)
	}
	return nil
}

// RemoveServiceMetadata strips labels + annotations from a Service.
func (k *KubeClient) RemoveServiceMetadata(ctx context.Context, namespace, name string, labelKeys, annotationKeys []string) error {
	patch := buildRemovePatch(labelKeys, annotationKeys)
	if patch == nil {
		return nil
	}
	_, err := k.Typed.CoreV1().Services(namespace).
		Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{
			FieldManager: AdoptionFieldManager,
		})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("strip metadata on service %q/%q: %w", namespace, name, err)
	}
	return nil
}

// buildRemovePatch assembles a JSON Merge Patch body that sets each label
// key and annotation key to null (i.e. removes them).
func buildRemovePatch(labelKeys, annotationKeys []string) []byte {
	if len(labelKeys) == 0 && len(annotationKeys) == 0 {
		return nil
	}
	metadata := map[string]interface{}{}
	if len(labelKeys) > 0 {
		labels := map[string]interface{}{}
		for _, k := range labelKeys {
			labels[k] = nil
		}
		metadata["labels"] = labels
	}
	if len(annotationKeys) > 0 {
		annotations := map[string]interface{}{}
		for _, k := range annotationKeys {
			annotations[k] = nil
		}
		metadata["annotations"] = annotations
	}
	patch := map[string]interface{}{"metadata": metadata}
	data, err := json.Marshal(patch)
	if err != nil {
		return nil
	}
	return data
}

// GVR is a convenience wrapper used by callers that don't want to import
// k8s.io/apimachinery/pkg/runtime/schema directly.
type GVR = schema.GroupVersionResource

func ptr[T any](v T) *T { return &v }
