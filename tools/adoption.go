// Package tools — adoption.go implements the adopt/reconcile/release workflow
// for bringing existing kubectl-managed Deployments into Epinio's view,
// plus the self_adoption capability the MCP uses to finish its own install
// after kubectl apply -f install/epinio-mcp.yaml.
//
// Adopted apps carry the `epinio.io/adopted: "true"` annotation on both the
// App CRD and the underlying Deployment. Epinio's label-based workload
// discovery still finds them (list/show/logs/exec work), but the MCP's own
// write tools refuse destructive operations on adopted apps — use kubectl
// for lifecycle or run `release_app` to convert back to pure k8s.
//
package tools

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/krumware/epinio-mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// --- Inputs / outputs ---

type AdoptAppInput struct {
	Namespace      string `json:"namespace" jsonschema:"the namespace containing the Deployment to adopt"`
	DeploymentName string `json:"deployment_name" jsonschema:"the existing Deployment to adopt"`
	AppName        string `json:"app_name,omitempty" jsonschema:"optional app name; defaults to deployment_name"`
	// ServiceName is optional — if supplied, the Service is also labeled.
	// If the Deployment has a matching Service (same namespace, same selector),
	// reconcile_app picks it up automatically; specify here when discovery
	// would be ambiguous.
	ServiceName string `json:"service_name,omitempty" jsonschema:"optional Service to also label; auto-discovered from Deployment selector when omitted"`
}

type AdoptAppOutput struct {
	Namespace  string   `json:"namespace"`
	AppName    string   `json:"app_name"`
	Deployment string   `json:"deployment"`
	Service    string   `json:"service,omitempty"`
	ImageURL   string   `json:"image_url,omitempty"`
	Routes     []string `json:"routes,omitempty"`
	Advisory   string   `json:"advisory"`
}

type ReconcileAppInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the adopted app"`
	Name      string `json:"name" jsonschema:"the app name to reconcile"`
	DryRun    bool   `json:"dry_run,omitempty" jsonschema:"report what would change without applying"`
}

type ReconcileAppOutput struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Changes   []string `json:"changes"`
	Applied   bool     `json:"applied"`
	ImageURL  string   `json:"image_url,omitempty"`
	Routes    []string `json:"routes,omitempty"`
}

type ReleaseAppInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the adopted app"`
	Name      string `json:"name" jsonschema:"the app name to release"`
}

type ReleaseAppOutput struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}

// --- Tool registration ---

func RegisterAdoptionTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "adopt_app",
			Annotations: &mcp.ToolAnnotations{Title: "Adopt App"},
			Description: "Bring an existing kubectl-managed Deployment into Epinio's " +
				"view. Labels the Deployment + pod template + Service with Epinio's " +
				"canonical labels, creates an App CRD with epinio.io/adopted=true, and " +
				"makes the app visible in `epinio app list`/`show`/`logs`. Destructive " +
				"Epinio operations are refused on adopted apps — use release_app to " +
				"remove the Epinio-side artifacts.",
		},
		adoptAppHandler(c),
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "reconcile_app",
			Annotations: &mcp.ToolAnnotations{Title: "Reconcile App"},
			Description: "Sync an adopted app's CRD to match observed reality — update " +
				"image URL from the Deployment's current image, derive routes from " +
				"Ingress hosts, and ensure labels/annotations are in place. Pass " +
				"dry_run=true to preview changes.",
		},
		reconcileAppHandler(c),
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "release_app",
			Annotations: &mcp.ToolAnnotations{Title: "Release App"},
			Description: "Remove Epinio-side artifacts for an adopted app: delete the " +
				"App CRD, strip canonical labels + epinio.io/adopted annotation from " +
				"the Deployment and Service. The underlying Deployment keeps running — " +
				"it just stops being visible to Epinio.",
		},
		releaseAppHandler(c),
	)
}

// --- Handlers ---

func adoptAppHandler(_ *client.Client) func(context.Context, *mcp.CallToolRequest, AdoptAppInput) (*mcp.CallToolResult, AdoptAppOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input AdoptAppInput) (*mcp.CallToolResult, AdoptAppOutput, error) {
		if input.Namespace == "" || input.DeploymentName == "" {
			return nil, AdoptAppOutput{}, fmt.Errorf("namespace and deployment_name are required")
		}
		appName := input.AppName
		if appName == "" {
			appName = input.DeploymentName
		}

		k, err := client.GetKubeClient()
		if err != nil {
			return nil, AdoptAppOutput{}, fmt.Errorf("kubernetes client: %w", err)
		}

		dep, err := k.GetDeployment(ctx, input.Namespace, input.DeploymentName)
		if err != nil {
			return nil, AdoptAppOutput{}, fmt.Errorf("get deployment %q/%q: %w", input.Namespace, input.DeploymentName, err)
		}

		image := primaryContainerImage(dep.Spec.Template.Spec.Containers)
		labels := client.CanonicalLabels(appName, input.Namespace)
		annotations := map[string]string{client.EpinioAdoptedAnnotation: "true"}
		podLabels := mergeMaps(labels, map[string]string{
			"epinio.io/app-container": appName,
			"epinio.io/stage-id":      "adopted",
		})

		if err := k.LabelDeployment(ctx, input.Namespace, input.DeploymentName, labels, annotations); err != nil {
			return nil, AdoptAppOutput{}, err
		}
		if err := k.LabelDeploymentPodTemplate(ctx, input.Namespace, input.DeploymentName, podLabels, nil); err != nil {
			return nil, AdoptAppOutput{}, err
		}

		// Service: caller-supplied or auto-discover by shared selector.
		serviceName := input.ServiceName
		if serviceName == "" {
			serviceName = discoverServiceForDeployment(ctx, k, input.Namespace, dep.Spec.Selector.MatchLabels)
		}
		if serviceName != "" {
			if err := k.LabelService(ctx, input.Namespace, serviceName, labels, annotations); err != nil {
				log.Printf("adopt_app: failed to label service %q/%q (non-fatal): %v", input.Namespace, serviceName, err)
				serviceName = ""
			}
		}

		routes := deriveRoutes(ctx, k, input.Namespace, serviceName)

		// App CRD — apply via server-side-apply. If one already exists, we
		// merge in our spec rather than overwrite (fieldManager lineage).
		app := buildAdoptedAppCR(input.Namespace, appName, image, routes, labels, annotations)
		if err := k.WriteEpinioApp(ctx, app); err != nil {
			return nil, AdoptAppOutput{}, err
		}

		return nil, AdoptAppOutput{
			Namespace:  input.Namespace,
			AppName:    appName,
			Deployment: input.DeploymentName,
			Service:    serviceName,
			ImageURL:   image,
			Routes:     routes,
			Advisory: "adopted — `epinio app list/show/logs/exec` work normally. " +
				"Destructive Epinio ops (update/restart/delete/configuration-bind) " +
				"are refused on adopted apps. Use `kubectl` for lifecycle or " +
				"`release_app` to unbind from Epinio.",
		}, nil
	}
}

func reconcileAppHandler(_ *client.Client) func(context.Context, *mcp.CallToolRequest, ReconcileAppInput) (*mcp.CallToolResult, ReconcileAppOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ReconcileAppInput) (*mcp.CallToolResult, ReconcileAppOutput, error) {
		if input.Namespace == "" || input.Name == "" {
			return nil, ReconcileAppOutput{}, fmt.Errorf("namespace and name are required")
		}
		k, err := client.GetKubeClient()
		if err != nil {
			return nil, ReconcileAppOutput{}, fmt.Errorf("kubernetes client: %w", err)
		}

		app, err := k.ReadEpinioApp(ctx, input.Namespace, input.Name)
		if err != nil {
			return nil, ReconcileAppOutput{}, fmt.Errorf("read app %q/%q: %w", input.Namespace, input.Name, err)
		}

		// Deployment is conventionally named the same as the app.
		dep, err := k.GetDeployment(ctx, input.Namespace, input.Name)
		if err != nil {
			return nil, ReconcileAppOutput{}, fmt.Errorf("get deployment %q/%q: %w", input.Namespace, input.Name, err)
		}
		observedImage := primaryContainerImage(dep.Spec.Template.Spec.Containers)

		labels := client.CanonicalLabels(input.Name, input.Namespace)
		annotations := map[string]string{client.EpinioAdoptedAnnotation: "true"}
		serviceName := discoverServiceForDeployment(ctx, k, input.Namespace, dep.Spec.Selector.MatchLabels)
		routes := deriveRoutes(ctx, k, input.Namespace, serviceName)

		// Diff against current CRD spec.
		currentImage, _, _ := unstructured.NestedString(app.Object, "spec", "imageurl")
		currentContainer, _, _ := unstructured.NestedString(app.Object, "spec", "origin", "container")
		currentRoutes, _, _ := unstructured.NestedStringSlice(app.Object, "spec", "routes")
		currentAnnotated := app.GetAnnotations()[client.EpinioAdoptedAnnotation] == "true"

		var changes []string
		if currentImage != observedImage {
			changes = append(changes, fmt.Sprintf("spec.imageurl: %q → %q", currentImage, observedImage))
		}
		if currentContainer != observedImage {
			changes = append(changes, fmt.Sprintf("spec.origin.container: %q → %q", currentContainer, observedImage))
		}
		if !stringSlicesEqual(currentRoutes, routes) {
			changes = append(changes, fmt.Sprintf("spec.routes: %v → %v", currentRoutes, routes))
		}
		if !currentAnnotated {
			changes = append(changes, fmt.Sprintf("annotations[%s]: set to \"true\"", client.EpinioAdoptedAnnotation))
		}

		out := ReconcileAppOutput{
			Namespace: input.Namespace,
			Name:      input.Name,
			Changes:   changes,
			ImageURL:  observedImage,
			Routes:    routes,
		}

		if input.DryRun || len(changes) == 0 {
			return nil, out, nil
		}

		// Apply the reconciled CR.
		newApp := buildAdoptedAppCR(input.Namespace, input.Name, observedImage, routes, labels, annotations)
		if err := k.WriteEpinioApp(ctx, newApp); err != nil {
			return nil, out, err
		}
		// Also ensure the Deployment carries the canonical labels + adopted
		// annotation. If the user or another controller clobbered them, we
		// restore.
		if err := k.LabelDeployment(ctx, input.Namespace, input.Name, labels, annotations); err != nil {
			log.Printf("reconcile_app: label deployment (non-fatal): %v", err)
		}
		out.Applied = true
		return nil, out, nil
	}
}

func releaseAppHandler(_ *client.Client) func(context.Context, *mcp.CallToolRequest, ReleaseAppInput) (*mcp.CallToolResult, ReleaseAppOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ReleaseAppInput) (*mcp.CallToolResult, ReleaseAppOutput, error) {
		if input.Namespace == "" || input.Name == "" {
			return nil, ReleaseAppOutput{}, fmt.Errorf("namespace and name are required")
		}
		k, err := client.GetKubeClient()
		if err != nil {
			return nil, ReleaseAppOutput{}, fmt.Errorf("kubernetes client: %w", err)
		}

		labelKeys := []string{
			"app.kubernetes.io/name",
			"app.kubernetes.io/part-of",
			"app.kubernetes.io/managed-by",
			"app.kubernetes.io/component",
		}
		annotationKeys := []string{client.EpinioAdoptedAnnotation}

		// Deployment is conventionally named like the app. Strip Epinio
		// metadata; the Deployment keeps running under whatever labels the
		// user applied originally.
		if err := k.RemoveDeploymentMetadata(ctx, input.Namespace, input.Name, labelKeys, annotationKeys); err != nil && !apierrors.IsNotFound(err) {
			log.Printf("release_app: strip deployment metadata (non-fatal): %v", err)
		}

		if dep, err := k.GetDeployment(ctx, input.Namespace, input.Name); err == nil {
			if svc := discoverServiceForDeployment(ctx, k, input.Namespace, dep.Spec.Selector.MatchLabels); svc != "" {
				if err := k.RemoveServiceMetadata(ctx, input.Namespace, svc, labelKeys, annotationKeys); err != nil && !apierrors.IsNotFound(err) {
					log.Printf("release_app: strip service metadata (non-fatal): %v", err)
				}
			}
		}

		// Delete the App CRD.
		if err := k.DeleteEpinioApp(ctx, input.Namespace, input.Name); err != nil {
			return nil, ReleaseAppOutput{}, err
		}

		return nil, ReleaseAppOutput{
			Namespace: input.Namespace,
			Name:      input.Name,
			Status: fmt.Sprintf(
				"released app %q from Epinio in namespace %q — underlying Deployment keeps running",
				input.Name, input.Namespace,
			),
		}, nil
	}
}

// --- Adopted-mode guard (exported for other tool handlers) ---

// EnsureNotAdopted returns a non-nil error when the target app is marked
// with epinio.io/adopted=true. Write-path tool handlers call this at the
// top to refuse destructive operations that would re-render the Helm
// chart and destroy the adopted install.
//
// If the K8s client isn't available (local dev, probe failure), the check
// is skipped with a warning log — fail-open so the MCP keeps working
// outside a cluster. The upstream server-side refusal (EPINIOROADMAP-56)
// is the authoritative protection.
func EnsureNotAdopted(ctx context.Context, namespace, name, operation string) error {
	k, err := client.GetKubeClient()
	if err != nil {
		log.Printf("adopted-mode guard: k8s client unavailable, skipping check: %v", err)
		return nil
	}
	adopted, err := k.IsAppAdopted(ctx, namespace, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// App doesn't exist as a CRD — let the downstream call handle it.
			return nil
		}
		log.Printf("adopted-mode guard: could not check app %q/%q: %v", namespace, name, err)
		return nil
	}
	if !adopted {
		return nil
	}
	return fmt.Errorf(
		"refusing %s on adopted app %q/%q: this app was not deployed through Epinio (no Helm release to re-render) — "+
			"use kubectl for lifecycle operations, or `release_app(namespace=%q, name=%q)` to convert back to plain k8s first",
		operation, namespace, name, namespace, name,
	)
}

// --- Shared helpers ---

func primaryContainerImage(containers []corev1.Container) string {
	for _, c := range containers {
		if c.Image != "" {
			return c.Image
		}
	}
	return ""
}

// buildAdoptedAppCR composes the unstructured App object we write for an
// adopted app. Kept deliberately minimal — only the spec fields the
// display/listing paths read.
func buildAdoptedAppCR(namespace, name, image string, routes []string, labels, annotations map[string]string) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"origin": map[string]interface{}{
			"container": image,
		},
		"imageurl":  image,
		"chartname": "",
		"stageid":   "adopted",
		"settings":  map[string]interface{}{},
	}
	if len(routes) > 0 {
		spec["routes"] = toInterfaceSlice(routes)
	} else {
		spec["routes"] = []interface{}{}
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "application.epinio.io/v1",
			"kind":       "App",
			"metadata": map[string]interface{}{
				"name":        name,
				"namespace":   namespace,
				"labels":      toStringMap(labels),
				"annotations": toStringMap(annotations),
			},
			"spec": spec,
		},
	}
	return obj
}

// discoverServiceForDeployment finds a Service in the same namespace whose
// selector is a subset of the Deployment's pod selector labels. Returns
// "" if none or if the discovery is ambiguous. Callers should pass an
// explicit name when ambiguity matters.
func discoverServiceForDeployment(ctx context.Context, k *client.KubeClient, namespace string, depSelector map[string]string) string {
	if len(depSelector) == 0 {
		return ""
	}
	svcs, err := k.Typed.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}
	var match string
	for _, svc := range svcs.Items {
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		if isSubsetOf(svc.Spec.Selector, depSelector) {
			if match != "" {
				// Ambiguous — caller must disambiguate.
				return ""
			}
			match = svc.Name
		}
	}
	return match
}

// deriveRoutes collects Ingress hosts whose backend references the given
// Service name. Returns "<host><path>" entries, or just "<host>" when the
// Ingress rule's path is "/" or empty (Epinio's route convention).
func deriveRoutes(ctx context.Context, k *client.KubeClient, namespace, serviceName string) []string {
	if serviceName == "" {
		return nil
	}
	ingresses, err := k.ListIngresses(ctx, namespace)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var routes []string
	for _, ing := range ingresses {
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, p := range rule.HTTP.Paths {
				if p.Backend.Service == nil || p.Backend.Service.Name != serviceName {
					continue
				}
				route := rule.Host
				if p.Path != "" && p.Path != "/" {
					route = rule.Host + p.Path
				}
				if route != "" && !seen[route] {
					seen[route] = true
					routes = append(routes, route)
				}
			}
		}
	}
	return routes
}

func mergeMaps(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func isSubsetOf(sub, super map[string]string) bool {
	for k, v := range sub {
		if super[k] != v {
			return false
		}
	}
	return true
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func toInterfaceSlice(s []string) []interface{} {
	out := make([]interface{}, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

func toStringMap(m map[string]string) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// describeAdopted builds a short human-readable hint explaining an adopted
// app's constraint set. Used by show_app's advisory field.
func describeAdopted() string {
	return strings.Join([]string{
		"adopted: deployed via kubectl (no Helm release owned by Epinio).",
		"Safe: list, show, logs, exec, port-forward.",
		"Refused: update_app, restart_app, delete_app, bind_configuration — they would re-render the chart and destroy the install.",
		"Use `kubectl` for lifecycle, or `release_app` to detach from Epinio.",
	}, " ")
}
