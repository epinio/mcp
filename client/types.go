package client

// InfoResponse is returned by GET /info.
type InfoResponse struct {
	Version             string `json:"version"`
	KubeVersion         string `json:"kube_version"`
	Platform            string `json:"platform"`
	DefaultBuilderImage string `json:"default_builder_image"`
	OIDCEnabled         bool   `json:"oidc_enabled"`
}

// Meta contains common metadata for namespaced resources.
type Meta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	CreatedAt string `json:"created_at"`
}

// MetaLite contains common metadata for non-namespaced resources.
type MetaLite struct {
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// Namespace represents an Epinio namespace.
type Namespace struct {
	Meta           MetaLite `json:"meta"`
	Apps           []string `json:"apps"`
	Configurations []string `json:"configurations"`
}

// App represents an Epinio application.
type App struct {
	Meta          Meta                     `json:"meta"`
	Configuration ApplicationConfiguration `json:"configuration"`
	Origin        ApplicationOrigin        `json:"origin"`
	Workload      *AppDeployment           `json:"deployment,omitempty"`
	Status        string                   `json:"status"`
	StatusMessage string                   `json:"status_message"`
	ImageURL      string                   `json:"image_url"`
	StageID       string                   `json:"stage_id"`
	// StagingStatus is Epinio's authoritative view of the staging Job.
	// Values: "active" (build in flight), "done" (built ok), "failed".
	// Populated from the K8s Job state — reliable even on first load,
	// unlike the client-side "stage_id changed recently" heuristic.
	// Note the JSON tag: Epinio uses one word, no underscore.
	StagingStatus string `json:"stagingstatus,omitempty"`
}

// ApplicationConfiguration holds app configuration.
type ApplicationConfiguration struct {
	Instances      *int32            `json:"instances,omitempty"`
	Configurations []string          `json:"configurations,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	Routes         []string          `json:"routes,omitempty"`
	AppChart       string            `json:"appchart,omitempty"`
	Settings       map[string]string `json:"settings,omitempty"`
}

// ApplicationOrigin describes how the app was created.
type ApplicationOrigin struct {
	Kind      int     `json:"kind"`
	Container string  `json:"container,omitempty"`
	Git       *GitRef `json:"git,omitempty"`
	Path      string  `json:"path,omitempty"`
}

// GitRef is a git reference.
type GitRef struct {
	URL      string `json:"url"`
	Branch   string `json:"branch"`
	Revision string `json:"revision"`
	Provider string `json:"provider"`
}

// AppDeployment contains deployment state.
// NOTE: Epinio API uses inconsistent JSON naming — these fields are "desiredreplicas"
// and "readyreplicas" (no underscore), not "desired_replicas" / "ready_replicas".
type AppDeployment struct {
	Name            string             `json:"name"`
	Active          bool               `json:"active"`
	DesiredReplicas int                `json:"desiredreplicas"`
	ReadyReplicas   int                `json:"readyreplicas"`
	Replicas        map[string]PodInfo `json:"replicas,omitempty"`
	Routes          []string           `json:"routes,omitempty"`
	StageID         string             `json:"stage_id"`
}

// PodInfo describes a running pod.
type PodInfo struct {
	Name       string `json:"name"`
	Ready      bool   `json:"ready"`
	Restarts   int    `json:"restarts"`
	MemoryUsed string `json:"memory_used"`
	CPUUsed    string `json:"cpu_used"`
}

// AppCreateRequest is the body for creating an app.
type AppCreateRequest struct {
	Name          string           `json:"name"`
	Configuration AppUpdateRequest `json:"configuration"`
}

// AppUpdateRequest is the body for updating an app.
type AppUpdateRequest struct {
	Restart        *bool             `json:"restart,omitempty"`
	Instances      *int32            `json:"instances,omitempty"`
	Configurations []string          `json:"configurations,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	Routes         []string          `json:"routes,omitempty"`
	AppChart       string            `json:"appchart,omitempty"`
	Settings       map[string]string `json:"settings,omitempty"`
}

// UploadResponse is returned after uploading app source.
type UploadResponse struct {
	BlobUID string `json:"blobuid"`
}

// StageRequest is the body for staging an app.
type StageRequest struct {
	App          AppRef `json:"app"`
	BlobUID      string `json:"blobuid"`
	BuilderImage string `json:"builderimage"`
}

// AppRef is a lightweight app reference.
type AppRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// StageResponse is returned after staging.
type StageResponse struct {
	Stage    StageRef `json:"stage"`
	ImageURL string   `json:"image"`
}

// StageRef identifies a staging run.
type StageRef struct {
	ID string `json:"id"`
}

// DeployRequest is the body for deploying an app.
type DeployRequest struct {
	App      AppRef            `json:"app"`
	Stage    StageRef          `json:"stage"`
	ImageURL string            `json:"image"`
	Origin   ApplicationOrigin `json:"origin"`
}

// DeployResponse is returned after deploying.
type DeployResponse struct {
	Routes []string `json:"routes"`
}

// EnvVariable represents an environment variable.
type EnvVariable struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Origin string `json:"origin,omitempty"`
}

// ConfigurationResponse is returned when listing configurations.
type ConfigurationResponse struct {
	Meta          Meta                      `json:"meta"`
	Configuration ConfigurationShowResponse `json:"configuration"`
}

// ConfigurationShowResponse holds configuration details.
type ConfigurationShowResponse struct {
	Details   map[string]string `json:"details,omitempty"`
	BoundApps []string          `json:"boundapps,omitempty"`
	Type      string            `json:"type"`
	Origin    string            `json:"origin"`
}

// ConfigurationCreateRequest is the body for creating a configuration.
type ConfigurationCreateRequest struct {
	Name string            `json:"name"`
	Data map[string]string `json:"data"`
}

// Service represents an Epinio service instance.
type Service struct {
	Meta                  Meta              `json:"meta"`
	CatalogService        string            `json:"catalog_service"`
	CatalogServiceVersion string            `json:"catalog_service_version"`
	Status                string            `json:"status"`
	BoundApps             []string          `json:"bound_apps"`
	InternalRoutes        []string          `json:"internal_routes"`
	Settings              map[string]string `json:"settings,omitempty"`
}

// CatalogService describes an available service from the catalog.
type CatalogService struct {
	Meta             MetaLite       `json:"meta"`
	Description      string         `json:"description"`
	ShortDescription string         `json:"short_description"`
	HelmChart        string         `json:"helm_chart"`
	ChartVersion     string         `json:"chart_version"`
	AppVersion       string         `json:"app_version"`
	HelmRepo         HelmRepo       `json:"helm_repo"`
	Settings         map[string]any `json:"settings,omitempty"`
}

// HelmRepo is a Helm repository reference.
type HelmRepo struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// AppChart describes an available Epinio application chart — the thing
// `epinio.yml: configuration.appchart` names. Mirrors upstream
// models.AppChart. Settings is a map of setting name → schema
// (type/minimum/maximum/enum).
type AppChart struct {
	Meta             MetaLite                `json:"meta,omitempty"`
	Description      string                  `json:"description,omitempty"`
	ShortDescription string                  `json:"short_description,omitempty"`
	HelmChart        string                  `json:"helm_chart,omitempty"`
	HelmRepo         string                  `json:"helm_repo,omitempty"`
	Settings         map[string]ChartSetting `json:"settings,omitempty"`
}

// ChartSetting describes a single configurable setting on an AppChart,
// including its type and (optional) value constraints. Mirrors upstream
// models.ChartSetting.
type ChartSetting struct {
	Type    string   `json:"type"`
	Minimum string   `json:"minimum,omitempty"`
	Maximum string   `json:"maximum,omitempty"`
	Enum    []string `json:"enum,omitempty"`
}

// ServiceCreateRequest is the body for creating a service.
type ServiceCreateRequest struct {
	CatalogService string            `json:"catalog_service"`
	Name           string            `json:"name"`
	Wait           bool              `json:"wait,omitempty"`
	Settings       map[string]string `json:"settings,omitempty"`
}

// ServiceBindRequest binds a service to an app.
type ServiceBindRequest struct {
	AppName string `json:"app_name"`
}

// ServiceUnbindRequest unbinds a service from an app.
type ServiceUnbindRequest struct {
	AppName string `json:"app_name"`
}

// PushResult contains the result of a full push workflow.
type PushResult struct {
	Routes  []string `json:"routes"`
	StageID string   `json:"stage_id"`
	Image   string   `json:"image"`
}
