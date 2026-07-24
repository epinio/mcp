package elevated

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// envOr returns the value of the environment variable key, or def when unset.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Capability is an optional feature set the MCP can provide once the
// underlying Epinio infrastructure is set up. Each capability declares its
// requirements; check_capabilities reports which are satisfied and which
// need action. enable_capability fulfills the satisfiable ones.
type Capability struct {
	Name        string
	Description string
	Requires    []Requirement
}

// ProbeContext carries per-call hints that environmental probes may use.
// Resource-backed requirements typically ignore it.
type ProbeContext struct {
	// ProbeApp is a "namespace/name" reference used by environmental probes
	// (e.g. log_streaming probing a specific app's WS endpoint).
	ProbeApp string
}

// Requirement is a single dependency that can be checked and, where the MCP
// has sufficient authority, fulfilled.
type Requirement interface {
	// Describe returns a stable short label (e.g. "self:adopted").
	Describe() string
	// Check evaluates whether the requirement is satisfied.
	Check(ctx context.Context, c *client.Client, pctx ProbeContext) RequirementStatus
	// Fulfill attempts to satisfy the requirement using the MCP's own auth.
	// Implementations for requirements that can't be fulfilled (probes, RBAC,
	// catalog CRDs without bundled manifests) return Action="not_fixable".
	Fulfill(ctx context.Context, c *client.Client, pctx ProbeContext) FulfillStatus
}

// diagnosticRequirement is the optional interface an environmental probe
// implements to opt out of being a hard gate for requireCapability. Probes
// are useful for reporting readiness via check_capabilities but they can
// flake (short handshake timeouts, pod-to-public-ingress hairpins, etc.)
// and shouldn't block tool execution when the downstream caller (often a
// browser on a different origin) can still succeed on its own.
type diagnosticRequirement interface {
	IsDiagnostic() bool
}

// RequirementStatus is the result of one check.
type RequirementStatus struct {
	Requirement string `json:"requirement"`
	Ready       bool   `json:"ready"`
	Message     string `json:"message"`
	// Mode is an optional quality label set by environmental probes — e.g.
	// "full" / "degraded" / "unavailable". Empty for resource-backed reqs.
	Mode string `json:"mode,omitempty"`
	// FixableByUs: true when enable_capability can satisfy this requirement
	// with the MCP's own auth. False when it needs admin intervention.
	FixableByUs bool   `json:"fixable_by_us"`
	Fix         string `json:"fix,omitempty"`
}

// FulfillStatus is the result of one fulfill attempt.
type FulfillStatus struct {
	Requirement string `json:"requirement"`
	// Action one of: "created", "bound", "no_op" (already satisfied),
	// "not_fixable" (admin required), "failed" (attempted but errored).
	Action  string `json:"action"`
	Ready   bool   `json:"ready"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// --- Resource-backed requirement types ---

// CatalogEntryReq asserts a catalog entry exists in the cluster.
// Catalog entries are cluster-scoped CRDs; installing them requires
// permission on services.application.epinio.io which the MCP pod typically
// doesn't have, so Fulfill returns "not_fixable" unless we later bundle
// manifests + grant the pod SA the appropriate RBAC.
type CatalogEntryReq struct {
	Name         string
	ManifestPath string // reserved for future self-install; unused today
}

func (r CatalogEntryReq) Describe() string { return "catalog:" + r.Name }

func (r CatalogEntryReq) Check(ctx context.Context, c *client.Client, _ ProbeContext) RequirementStatus {
	if _, err := c.ShowCatalogService(r.Name); err != nil {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("catalog entry %q not found: %v", r.Name, err),
			FixableByUs: false,
			Fix: fmt.Sprintf(
				"kubectl apply -f manifests/%s-catalog-entry.yaml (cluster admin)",
				r.Name,
			),
		}
	}
	return RequirementStatus{
		Requirement: r.Describe(),
		Ready:       true,
		Message:     fmt.Sprintf("catalog entry %q is installed", r.Name),
	}
}

func (r CatalogEntryReq) Fulfill(ctx context.Context, c *client.Client, _ ProbeContext) FulfillStatus {
	st := r.Check(ctx, c, ProbeContext{})
	if st.Ready {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "no_op",
			Ready:       true,
			Message:     st.Message,
		}
	}
	return FulfillStatus{
		Requirement: r.Describe(),
		Action:      "not_fixable",
		Ready:       false,
		Message:     "catalog entries must be installed by a cluster admin — the MCP pod ServiceAccount is not granted CRD-level permissions by default",
	}
}

// ServiceInstanceReq asserts a service instance exists in a namespace.
type ServiceInstanceReq struct {
	Namespace string
	Name      string
	From      string // catalog service name (used by Fulfill)
}

func (r ServiceInstanceReq) Describe() string {
	return fmt.Sprintf("service:%s/%s", r.Namespace, r.Name)
}

func (r ServiceInstanceReq) Check(ctx context.Context, c *client.Client, _ ProbeContext) RequirementStatus {
	services, err := c.ListServices(r.Namespace)
	if err != nil {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("could not list services in %q: %v", r.Namespace, err),
		}
	}
	for _, s := range services {
		if s.Meta.Name == r.Name {
			return RequirementStatus{
				Requirement: r.Describe(),
				Ready:       true,
				Message:     fmt.Sprintf("service %q present in %q", r.Name, r.Namespace),
			}
		}
	}
	return RequirementStatus{
		Requirement: r.Describe(),
		Ready:       false,
		Message:     fmt.Sprintf("service %q not found in %q", r.Name, r.Namespace),
		FixableByUs: true,
		Fix:         fmt.Sprintf(`create_service(namespace=%q, name=%q, catalog_service=%q)`, r.Namespace, r.Name, r.From),
	}
}

func (r ServiceInstanceReq) Fulfill(ctx context.Context, c *client.Client, _ ProbeContext) FulfillStatus {
	if st := r.Check(ctx, c, ProbeContext{}); st.Ready {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "no_op",
			Ready:       true,
			Message:     st.Message,
		}
	}
	if err := c.CreateService(r.Namespace, client.ServiceCreateRequest{
		CatalogService: r.From,
		Name:           r.Name,
		Wait:           true,
	}); err != nil {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "failed",
			Ready:       false,
			Message:     fmt.Sprintf("could not create service %q from %q in %q", r.Name, r.From, r.Namespace),
			Error:       err.Error(),
		}
	}
	return FulfillStatus{
		Requirement: r.Describe(),
		Action:      "created",
		Ready:       true,
		Message:     fmt.Sprintf("created service %q from catalog %q in namespace %q", r.Name, r.From, r.Namespace),
	}
}

// ConfigurationBindingReq asserts a named Epinio configuration is bound to a
// specific app. Zero values for App/Namespace resolve from env vars
// (EPINIO_MCP_APP_NAMESPACE / EPINIO_MCP_APP_NAME) with defaults
// "epinio" / "epinio-mcp".
type ConfigurationBindingReq struct {
	Namespace     string
	App           string
	Configuration string
}

func (r ConfigurationBindingReq) resolve() (namespace, app string) {
	namespace = r.Namespace
	app = r.App
	if namespace == "" {
		namespace = envOr("EPINIO_MCP_APP_NAMESPACE", "epinio")
	}
	if app == "" {
		app = envOr("EPINIO_MCP_APP_NAME", "epinio-mcp")
	}
	return
}

func (r ConfigurationBindingReq) Describe() string {
	ns, app := r.resolve()
	return fmt.Sprintf("binding:%s→%s/%s", r.Configuration, ns, app)
}

func (r ConfigurationBindingReq) Check(ctx context.Context, c *client.Client, _ ProbeContext) RequirementStatus {
	ns, app := r.resolve()
	a, err := c.ShowApp(ns, app)
	if err != nil {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("could not read app %q in %q: %v", app, ns, err),
		}
	}
	for _, cfg := range a.Configuration.Configurations {
		if cfg == r.Configuration {
			return RequirementStatus{
				Requirement: r.Describe(),
				Ready:       true,
				Message:     fmt.Sprintf("configuration %q is bound to %s/%s", r.Configuration, ns, app),
			}
		}
	}
	return RequirementStatus{
		Requirement: r.Describe(),
		Ready:       false,
		Message:     fmt.Sprintf("configuration %q not bound to %s/%s", r.Configuration, ns, app),
		FixableByUs: true,
		Fix:         fmt.Sprintf(`bind_configuration(namespace=%q, app=%q, configurations=[%q])`, ns, app, r.Configuration),
	}
}

func (r ConfigurationBindingReq) Fulfill(ctx context.Context, c *client.Client, _ ProbeContext) FulfillStatus {
	ns, app := r.resolve()
	if st := r.Check(ctx, c, ProbeContext{}); st.Ready {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "no_op",
			Ready:       true,
			Message:     st.Message,
		}
	}
	if err := c.BindConfiguration(ns, app, []string{r.Configuration}); err != nil {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "failed",
			Ready:       false,
			Message:     fmt.Sprintf("could not bind %q to %s/%s", r.Configuration, ns, app),
			Error:       err.Error(),
		}
	}
	return FulfillStatus{
		Requirement: r.Describe(),
		Action:      "bound",
		Ready:       true,
		Message:     fmt.Sprintf("bound configuration %q to %s/%s — restart the app for it to take effect", r.Configuration, ns, app),
	}
}

// PodRBACReq asserts the MCP pod's ServiceAccount has a K8s permission.
// Always FixableByUs=false — granting RBAC is an admin operation.
type PodRBACReq struct {
	Verb      string
	Group     string
	Resource  string
	Namespace string // optional; empty for cluster-scope
}

func (r PodRBACReq) Describe() string {
	return fmt.Sprintf("rbac:%s/%s:%s", r.Group, r.Resource, r.Verb)
}

func (r PodRBACReq) Check(ctx context.Context, _ *client.Client, _ ProbeContext) RequirementStatus {
	k, err := GetKubeClient()
	if err != nil {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("cannot reach Kubernetes API from MCP pod: %v", err),
		}
	}
	allowed, err := k.CanI(ctx, r.Verb, r.Group, r.Resource, r.Namespace)
	if err != nil {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("SelfSubjectAccessReview failed: %v", err),
		}
	}
	if !allowed {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("MCP pod ServiceAccount is not permitted to %s %s/%s", r.Verb, r.Group, r.Resource),
			FixableByUs: false,
			Fix: fmt.Sprintf(
				"grant the MCP pod ServiceAccount %q on %s/%s via ClusterRole + ClusterRoleBinding (see manifests/epinio-mcp-rbac.yaml)",
				r.Verb, r.Group, r.Resource,
			),
		}
	}
	return RequirementStatus{
		Requirement: r.Describe(),
		Ready:       true,
		Message:     fmt.Sprintf("MCP pod can %s %s/%s", r.Verb, r.Group, r.Resource),
	}
}

func (r PodRBACReq) Fulfill(ctx context.Context, c *client.Client, _ ProbeContext) FulfillStatus {
	st := r.Check(ctx, c, ProbeContext{})
	if st.Ready {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "no_op",
			Ready:       true,
			Message:     st.Message,
		}
	}
	return FulfillStatus{
		Requirement: r.Describe(),
		Action:      "not_fixable",
		Ready:       false,
		Message:     st.Message,
	}
}

// SelfAdoptionReq asserts the MCP's own App CRD has the metadata an adopted
// app should carry — the canonical labels, the `epinio.io/adopted: "true"`
// annotation, and a populated spec.imageurl matching the running pod. It's
// always resolved against the MCP's self-identity env vars.
type SelfAdoptionReq struct{}

func (r SelfAdoptionReq) Describe() string { return "self:adopted" }

func (r SelfAdoptionReq) selfRef() (namespace, name string) {
	namespace = envOr("EPINIO_MCP_APP_NAMESPACE", "epinio")
	name = envOr("EPINIO_MCP_APP_NAME", "epinio-mcp")
	return
}

func (r SelfAdoptionReq) Check(ctx context.Context, _ *client.Client, _ ProbeContext) RequirementStatus {
	namespace, name := r.selfRef()
	k, err := GetKubeClient()
	if err != nil {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("K8s client unavailable: %v", err),
			FixableByUs: false,
			Fix:         "run the MCP inside the cluster or set EPINIO_MCP_APP_NAMESPACE/_NAME to override self-identity",
		}
	}
	app, err := k.ReadEpinioApp(ctx, namespace, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return RequirementStatus{
				Requirement: r.Describe(),
				Ready:       false,
				Message:     fmt.Sprintf("MCP's own App CRD %q/%q is missing — the install manifest should have created it", namespace, name),
				FixableByUs: true,
				Fix:         "enable_capability(name=\"self_adoption\") — creates the App CRD from the running Deployment",
			}
		}
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("read MCP's App CRD: %v", err),
		}
	}
	annotations := app.GetAnnotations()
	if annotations[EpinioAdoptedAnnotation] != "true" {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("App CRD %q/%q is not annotated as adopted", namespace, name),
			FixableByUs: true,
			Fix:         "enable_capability(name=\"self_adoption\")",
		}
	}
	imageurl, _, _ := unstructured.NestedString(app.Object, "spec", "imageurl")
	if imageurl == "" {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Message:     fmt.Sprintf("App CRD %q/%q has empty spec.imageurl — reconcile_app would populate it from the running Deployment", namespace, name),
			FixableByUs: true,
			Fix:         "enable_capability(name=\"self_adoption\")",
		}
	}
	return RequirementStatus{
		Requirement: r.Describe(),
		Ready:       true,
		Message:     fmt.Sprintf("MCP self-adoption metadata is complete on %q/%q", namespace, name),
	}
}

func (r SelfAdoptionReq) Fulfill(ctx context.Context, c *client.Client, pctx ProbeContext) FulfillStatus {
	namespace, name := r.selfRef()
	if st := r.Check(ctx, c, pctx); st.Ready {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "no_op",
			Ready:       true,
			Message:     st.Message,
		}
	}
	out := adoptOrReconcileSelf(ctx, namespace, name)
	if out.err != nil {
		return FulfillStatus{
			Requirement: r.Describe(),
			Action:      "failed",
			Ready:       false,
			Message:     out.message,
			Error:       out.err.Error(),
		}
	}
	return FulfillStatus{
		Requirement: r.Describe(),
		Action:      out.action,
		Ready:       true,
		Message:     out.message,
	}
}

// adoptOrReconcileSelf ensures the MCP's own App CRD exists, is marked
// adopted, and has a spec.imageurl matching the running Deployment. Used
// by SelfAdoptionReq.Fulfill. Returned struct is small and local to keep
// the caller shape simple.
type selfAdoptResult struct {
	action  string // "created" | "patched"
	message string
	err     error
}

func adoptOrReconcileSelf(ctx context.Context, namespace, name string) selfAdoptResult {
	k, err := GetKubeClient()
	if err != nil {
		return selfAdoptResult{message: "K8s client unavailable", err: err}
	}

	dep, err := k.GetDeployment(ctx, namespace, name)
	if err != nil {
		return selfAdoptResult{
			message: fmt.Sprintf("read deployment %q/%q", namespace, name),
			err:     err,
		}
	}
	image := primaryContainerImage(dep.Spec.Template.Spec.Containers)

	labels := CanonicalLabels(name, namespace)
	annotations := map[string]string{EpinioAdoptedAnnotation: "true"}
	serviceName := discoverServiceForDeployment(ctx, k, namespace, dep.Spec.Selector.MatchLabels)
	routes := deriveRoutes(ctx, k, namespace, serviceName)

	// Ensure the App CRD is present and correct.
	app := buildAdoptedAppCR(namespace, name, image, routes, labels, annotations)
	if err := k.WriteEpinioApp(ctx, app); err != nil {
		return selfAdoptResult{message: "write app CRD", err: err}
	}

	// Make sure the Deployment and Service are labeled too — users may
	// have stripped labels by mistake, or an older install didn't set
	// the adoption annotation.
	if err := k.LabelDeployment(ctx, namespace, name, labels, annotations); err != nil {
		return selfAdoptResult{message: "label deployment", err: err}
	}
	podLabels := mergeMaps(labels, map[string]string{
		"epinio.io/app-container": name,
		"epinio.io/stage-id":      "adopted",
	})
	if err := k.LabelDeploymentPodTemplate(ctx, namespace, name, podLabels, nil); err != nil {
		return selfAdoptResult{message: "label pod template", err: err}
	}
	if serviceName != "" {
		_ = k.LabelService(ctx, namespace, serviceName, labels, annotations)
	}

	return selfAdoptResult{
		action: "created",
		message: fmt.Sprintf(
			"self-adoption metadata ensured on %q/%q (image=%s, routes=%v)",
			namespace, name, image, routes,
		),
	}
}

// --- Environmental probe requirement types ---

// LogStreamProbeReq probes whether WebSocket log streaming is reachable from
// the MCP pod. ProbeContext.ProbeApp overrides the defaults here.
type LogStreamProbeReq struct {
	DefaultNamespace string
	DefaultApp       string
}

func (r LogStreamProbeReq) Describe() string { return "probe:log_streaming" }

// IsDiagnostic marks this requirement as informational-only for the gate
// path. check_capabilities still reports its mode/ready for observability,
// but requireCapability won't block tool execution on a flaky probe.
func (r LogStreamProbeReq) IsDiagnostic() bool { return true }

func (r LogStreamProbeReq) Check(ctx context.Context, c *client.Client, pctx ProbeContext) RequirementStatus {
	ns, app := r.DefaultNamespace, r.DefaultApp
	if pctx.ProbeApp != "" {
		if parts := strings.SplitN(pctx.ProbeApp, "/", 2); len(parts) == 2 {
			ns, app = parts[0], parts[1]
		}
	}
	if ns == "" || app == "" {
		return RequirementStatus{
			Requirement: r.Describe(),
			Ready:       false,
			Mode:        "unavailable",
			Message:     "no probe target — pass probe_app as 'namespace/name'",
		}
	}

	mode, msg := c.ProbeLogStream(ns, app)
	return RequirementStatus{
		Requirement: r.Describe(),
		Ready:       mode != "unavailable",
		Mode:        mode,
		Message:     msg,
		FixableByUs: false,
		Fix:         "WebSocket path depends on ingress config — ensure Upgrade headers survive end-to-end",
	}
}

func (r LogStreamProbeReq) Fulfill(ctx context.Context, c *client.Client, pctx ProbeContext) FulfillStatus {
	st := r.Check(ctx, c, pctx)
	return FulfillStatus{
		Requirement: r.Describe(),
		Action:      "not_fixable",
		Ready:       st.Ready,
		Message:     "environmental probe — nothing to install",
	}
}

// --- Registry ---

var registry = map[string]Capability{
	"log_streaming": {
		Name: "log_streaming",
		Description: "Live log tailing via WebSocket. Falls back to HTTP polling " +
			"when the WS handshake fails (commonly when ingress controllers strip " +
			"Upgrade headers). Degraded mode still returns historical logs.",
		Requires: []Requirement{
			LogStreamProbeReq{
				DefaultNamespace: "epinio",
				DefaultApp:       "epinio-mcp",
			},
		},
	},
	"self_adoption": {
		Name: "self_adoption",
		Description: "MCP's own App CRD is complete and adopted. Ensures the " +
			"CRD exists, carries the epinio.io/adopted annotation, has labels " +
			"matching the running Deployment, and a spec.imageurl that reflects " +
			"the actual running image. Fulfillment reconciles any of these.",
		Requires: []Requirement{
			SelfAdoptionReq{},
		},
	},
}

// Capabilities returns a snapshot of the registry. Exposed for tests and
// for the requireCapability gate in tool handlers.
func Capabilities() map[string]Capability {
	out := make(map[string]Capability, len(registry))
	for k, v := range registry {
		out[k] = v
	}
	return out
}

// --- Check / enable tools ---

type CheckCapabilitiesInput struct {
	Name     string `json:"name,omitempty" jsonschema:"optional capability name; if empty, checks all"`
	ProbeApp string `json:"probe_app,omitempty" jsonschema:"optional 'namespace/name' hint used by environmental probes"`
}

type CapabilityStatus struct {
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Ready        bool                `json:"ready"`
	Mode         string              `json:"mode,omitempty"`
	Requirements []RequirementStatus `json:"requirements"`
}

type CheckCapabilitiesOutput struct {
	Capabilities []CapabilityStatus `json:"capabilities"`
}

type EnableCapabilityInput struct {
	Name     string `json:"name" jsonschema:"the capability to enable"`
	ProbeApp string `json:"probe_app,omitempty" jsonschema:"optional 'namespace/name' hint for any environmental probes triggered by Fulfill"`
}

type EnableCapabilityOutput struct {
	Name         string              `json:"name"`
	Ready        bool                `json:"ready"`
	Fulfilled    []FulfillStatus     `json:"fulfilled"`
	StillMissing []RequirementStatus `json:"still_missing,omitempty"`
	NeedsAdmin   []RequirementStatus `json:"needs_admin,omitempty"`
}

// CheckCapability runs Check on every requirement of a capability and
// aggregates the result. Exposed so requireCapability (gate in other tool
// handlers) can reuse the same logic.
func CheckCapability(ctx context.Context, c *client.Client, cap Capability, pctx ProbeContext) CapabilityStatus {
	status := CapabilityStatus{
		Name:        cap.Name,
		Description: cap.Description,
		Ready:       true,
	}
	for _, req := range cap.Requires {
		rs := req.Check(ctx, c, pctx)
		status.Requirements = append(status.Requirements, rs)
		if !rs.Ready {
			status.Ready = false
		}
		if status.Mode == "" && rs.Mode != "" {
			status.Mode = rs.Mode
		}
	}
	return status
}

// MissingCapabilityError is returned by requireCapability when a gated tool
// is called before its capability's prerequisites are satisfied. The handler
// turns this into a structured MCP tool error with a fix hint in the body.
type MissingCapabilityError struct {
	Capability string
	Missing    []RequirementStatus
}

func (e *MissingCapabilityError) Error() string {
	var labels []string
	for _, r := range e.Missing {
		labels = append(labels, r.Requirement)
	}
	return fmt.Sprintf("capability %q not ready: missing %s — call enable_capability(%q) or check_capabilities(%q)",
		e.Capability, strings.Join(labels, ", "), e.Capability, e.Capability)
}

// requireCapability returns nil if the named capability is ready, or a
// MissingCapabilityError containing the failing requirements if not.
// Intended for use at the top of gated tool handlers.
func requireCapability(ctx context.Context, c *client.Client, name string, pctx ProbeContext) error {
	cap, ok := registry[name]
	if !ok {
		return fmt.Errorf("unknown capability %q", name)
	}
	// Only gate on real (non-diagnostic) requirements. Probes exist to report
	// reachability through check_capabilities; they can flake without meaning
	// the caller's own attempt will fail, so they're not a hard gate.
	var missing []RequirementStatus
	for _, req := range cap.Requires {
		if isDiagnosticRequirement(req) {
			continue
		}
		rs := req.Check(ctx, c, pctx)
		if !rs.Ready {
			missing = append(missing, rs)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingCapabilityError{Capability: name, Missing: missing}
}

// isDiagnosticRequirement returns true when a requirement opted out of being
// a hard gate by implementing diagnosticRequirement.IsDiagnostic() → true.
func isDiagnosticRequirement(r Requirement) bool {
	d, ok := r.(diagnosticRequirement)
	return ok && d.IsDiagnostic()
}

// RegisterCapabilityTools adds check_capabilities and enable_capability.
func RegisterCapabilityTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "check_capabilities",
			Annotations: &mcp.ToolAnnotations{Title: "Check Capabilities"},
			Description: "Report which optional MCP capabilities are ready and what's " +
				"missing for the ones that aren't. Supply name to check a single " +
				"capability; supply probe_app (namespace/name) to target environmental " +
				"probes at a specific app.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CheckCapabilitiesInput,
		) (*mcp.CallToolResult, CheckCapabilitiesOutput, error) {
			pctx := ProbeContext{ProbeApp: input.ProbeApp}
			out := CheckCapabilitiesOutput{}
			if input.Name != "" {
				cap, ok := registry[input.Name]
				if !ok {
					return nil, out, fmt.Errorf("unknown capability %q", input.Name)
				}
				out.Capabilities = []CapabilityStatus{CheckCapability(ctx, c, cap, pctx)}
				return nil, out, nil
			}
			out.Capabilities = make([]CapabilityStatus, 0, len(registry))
			for _, cap := range registry {
				out.Capabilities = append(out.Capabilities, CheckCapability(ctx, c, cap, pctx))
			}
			return nil, out, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "enable_capability",
			Annotations: &mcp.ToolAnnotations{Title: "Enable Capability"},
			Description: "Attempt to satisfy a capability's requirements using the " +
				"MCP's own auth. Creates service instances and configuration bindings " +
				"as needed. Reports anything that needs cluster-admin intervention " +
				"under needs_admin.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input EnableCapabilityInput,
		) (*mcp.CallToolResult, EnableCapabilityOutput, error) {
			cap, ok := registry[input.Name]
			if !ok {
				return nil, EnableCapabilityOutput{}, fmt.Errorf("unknown capability %q", input.Name)
			}
			pctx := ProbeContext{ProbeApp: input.ProbeApp}
			out := EnableCapabilityOutput{Name: cap.Name}

			for _, reqr := range cap.Requires {
				// Check first — we skip Fulfill for already-ready requirements so
				// the report cleanly shows "created" / "bound" only for new work.
				pre := reqr.Check(ctx, c, pctx)
				if pre.Ready {
					out.Fulfilled = append(out.Fulfilled, FulfillStatus{
						Requirement: pre.Requirement,
						Action:      "no_op",
						Ready:       true,
						Message:     pre.Message,
					})
					continue
				}
				fs := reqr.Fulfill(ctx, c, pctx)
				out.Fulfilled = append(out.Fulfilled, fs)
				if fs.Ready {
					continue
				}
				// Not ready after attempt — decide which bucket it lands in.
				if fs.Action == "not_fixable" {
					out.NeedsAdmin = append(out.NeedsAdmin, pre)
				} else {
					out.StillMissing = append(out.StillMissing, pre)
				}
			}

			// Final readiness: re-check to account for side effects (e.g. binding
			// needing an app restart — the binding is present but the pod hasn't
			// picked it up yet; downstream checks may still fail and that's honest).
			final := CheckCapability(ctx, c, cap, pctx)
			out.Ready = final.Ready
			return nil, out, nil
		},
	)
}
