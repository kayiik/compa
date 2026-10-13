package modelservice

// Managing provider instances: creating, updating and removing one, syncing
// its catalog, and the views of the instances, their catalogs and their
// targets. The launcher's /api/provider-instances routes and the
// compa-kernel model commands share these, so both change the config, the
// auth store and the catalogs the same way.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	coreproviders "github.com/kayiik/llmgw-core/providers"

	"github.com/kayiik/compa/pkg/auth"
	"github.com/kayiik/compa/pkg/config"
)

// The kinds of InstanceError. errors.Is(err, ErrInstanceNotFound) and so on
// tell them apart.
var (
	// ErrInstanceInvalid: the input or the config it makes does not validate.
	ErrInstanceInvalid = errors.New("invalid provider instance")
	// ErrInstanceNotFound: no instance has the id.
	ErrInstanceNotFound = errors.New("provider instance not found")
	// ErrInstanceConflict: the instance exists already, or routes name it.
	ErrInstanceConflict = errors.New("provider instance conflict")
	// ErrInstanceDisabled: the instance is disabled.
	ErrInstanceDisabled = errors.New("provider instance is disabled")
	// ErrInstanceUnsupported: the instance cannot sync its catalog.
	ErrInstanceUnsupported = errors.New("provider instance cannot do this")
	// ErrInstanceUpstream: the provider or the credential store failed.
	ErrInstanceUpstream = errors.New("provider instance upstream failure")
	// ErrInstanceNotConfigured: no catalog sync driver was given.
	ErrInstanceNotConfigured = errors.New("provider instance operation is not configured")
	// ErrInstanceInternal: reading or writing the config, the catalogs or the
	// auth store failed.
	ErrInstanceInternal = errors.New("provider instance internal failure")
)

// InstanceError is why an instance operation failed. Message is the whole
// sentence to show, as the launcher's API answers it; Kind is one of the
// ErrInstance* errors; Routes are the routes that name the instance, for a
// conflict that they cause.
type InstanceError struct {
	Kind    error
	Message string
	Routes  []string
	Err     error
}

func (e *InstanceError) Error() string { return e.Message }

// Is matches the error's Kind.
func (e *InstanceError) Is(target error) bool { return target == e.Kind }

func (e *InstanceError) Unwrap() error { return e.Err }

func instanceErr(kind error, cause error, format string, args ...any) *InstanceError {
	return &InstanceError{Kind: kind, Message: fmt.Sprintf(format, args...), Err: cause}
}

// InstanceInput describes a provider instance to create, or what an update
// sets: the fields of the launcher's create and update request bodies.
// AuthConnectionRef, APIKey, Headers and Settings are write-only; nil leaves
// them as they are. A non-blank APIKey is stored in the auth store under
// InstanceCredentialKey(ID), and the instance authenticates with it. Runtime
// nil keeps an updated instance's runtime settings; {} resets them.
type InstanceInput struct {
	ID                string
	ProviderKind      string
	Adapter           string
	Protocol          string
	Endpoint          string
	Runtime           *config.ProviderInstanceRuntime
	State             config.ProviderInstanceState
	AuthConnectionRef *string
	APIKey            *string
	Headers           *map[string]string
	Settings          *map[string]any
	ClearAuth         bool
	ClearHeaders      bool
	ClearSettings     bool
}

func (in InstanceInput) apiKey() string {
	if in.APIKey == nil {
		return ""
	}
	return strings.TrimSpace(*in.APIKey)
}

// InstanceCredentialKey is the auth store key of the API key stored for the
// instance id.
func InstanceCredentialKey(id string) string { return "provider-" + id }

// InstanceDeps are the effects of the instance operations. A nil credential
// function uses the auth store. SyncCatalog and ResolveCredential have no
// default: DefaultInstanceDeps fills them.
type InstanceDeps struct {
	GetCredential     func(key string) (*auth.AuthCredential, error)
	SetCredential     func(key string, cred *auth.AuthCredential) error
	DeleteCredential  func(key string) error
	ResolveCredential func(ref string) (string, error)
	SyncCatalog       CatalogSyncFunc
}

// DefaultInstanceDeps are the production effects: the auth store,
// ResolveCredentialReference and SyncCatalog.
func DefaultInstanceDeps() InstanceDeps {
	return InstanceDeps{ResolveCredential: ResolveCredentialReference, SyncCatalog: SyncCatalog}.withDefaults()
}

func (d InstanceDeps) withDefaults() InstanceDeps {
	if d.GetCredential == nil {
		d.GetCredential = auth.GetCredential
	}
	if d.SetCredential == nil {
		d.SetCredential = auth.SetCredential
	}
	if d.DeleteCredential == nil {
		d.DeleteCredential = auth.DeleteCredential
	}
	return d
}

// InstanceFromCreateInput returns the instance input describes, validated.
// The API key is not applied.
func InstanceFromCreateInput(input InstanceInput) (*config.ProviderInstanceConfig, error) {
	if input.ClearAuth || input.ClearHeaders || input.ClearSettings {
		return nil, fmt.Errorf("clear fields are only valid when updating an existing provider instance")
	}
	instance := publicInstanceFromInput(input)
	if input.AuthConnectionRef != nil {
		instance.AuthConnectionRef = *input.AuthConnectionRef
	}
	if input.Headers != nil {
		instance.Headers = CloneStringMap(*input.Headers)
	}
	if input.Settings != nil {
		instance.Settings = CloneAnyMap(*input.Settings)
	}
	if err := ValidateInstance(instance); err != nil {
		return nil, err
	}
	return instance, nil
}

// InstanceFromUpdateInput returns existing as input updates it, validated.
// The API key is not applied.
func InstanceFromUpdateInput(input InstanceInput, existing *config.ProviderInstanceConfig) (*config.ProviderInstanceConfig, error) {
	instance := publicInstanceFromInput(input)
	if input.Runtime == nil {
		instance.Runtime = CloneRuntime(existing.Runtime)
	} else {
		KeepRedactedProxy(instance.Runtime, existing.Runtime)
	}
	instance.AuthConnectionRef = existing.AuthConnectionRef
	instance.Headers = CloneStringMap(existing.Headers)
	instance.Settings = CloneAnyMap(existing.Settings)
	if input.ClearAuth && input.AuthConnectionRef != nil ||
		input.ClearHeaders && input.Headers != nil ||
		input.ClearSettings && input.Settings != nil {
		return nil, fmt.Errorf("a sensitive field cannot be replaced and cleared in the same request")
	}
	if input.AuthConnectionRef != nil {
		instance.AuthConnectionRef = *input.AuthConnectionRef
	}
	if input.Headers != nil {
		instance.Headers = CloneStringMap(*input.Headers)
	}
	if input.Settings != nil {
		instance.Settings = CloneAnyMap(*input.Settings)
	}
	if input.ClearAuth {
		instance.AuthConnectionRef = ""
	}
	if input.ClearHeaders {
		instance.Headers = nil
	}
	if input.ClearSettings {
		instance.Settings = nil
	}
	if err := ValidateInstance(instance); err != nil {
		return nil, err
	}
	return instance, nil
}

// InputFromInstance is the update input that keeps instance as it is: its
// public fields, with every write-only field left unchanged.
func InputFromInstance(instance *config.ProviderInstanceConfig) InstanceInput {
	return InstanceInput{
		ID: instance.ID, ProviderKind: instance.ProviderKind, Adapter: instance.Adapter,
		Protocol: instance.Protocol, Endpoint: instance.Endpoint, State: instance.State,
	}
}

func publicInstanceFromInput(input InstanceInput) *config.ProviderInstanceConfig {
	return &config.ProviderInstanceConfig{
		ID: input.ID, ProviderKind: input.ProviderKind, Adapter: input.Adapter,
		Protocol: input.Protocol, Endpoint: input.Endpoint,
		Runtime: NormalizeRuntime(input.Runtime), State: input.State,
	}
}

// ValidateInstance checks an instance as the API accepts it: valid, with an
// adapter Compa runs and, when it has one, an absolute endpoint URL without
// userinfo or query.
func ValidateInstance(instance *config.ProviderInstanceConfig) error {
	if err := instance.Validate(); err != nil {
		return err
	}
	if err := config.SupportedProviderInstanceAdapter(instance); err != nil {
		return err
	}
	endpoint := strings.TrimSpace(instance.Endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("endpoint must be an absolute URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("endpoint must not contain URL userinfo")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return fmt.Errorf("endpoint must not contain a query string")
	}
	return nil
}

// NormalizeRuntime returns a copy of runtime with its text settings trimmed,
// or nil when every setting is the provider default.
func NormalizeRuntime(runtime *config.ProviderInstanceRuntime) *config.ProviderInstanceRuntime {
	normalized := CloneRuntime(runtime)
	if normalized == nil {
		return nil
	}
	normalized.Proxy = strings.TrimSpace(normalized.Proxy)
	normalized.ThinkingLevel = strings.ToLower(strings.TrimSpace(normalized.ThinkingLevel))
	normalized.MaxTokensField = strings.TrimSpace(normalized.MaxTokensField)
	normalized.ToolSchemaTransform = strings.ToLower(strings.TrimSpace(normalized.ToolSchemaTransform))
	if len(normalized.ExtraBody) == 0 {
		normalized.ExtraBody = nil
	}
	if reflect.DeepEqual(*normalized, config.ProviderInstanceRuntime{}) {
		return nil
	}
	return normalized
}

// CloneRuntime returns a deep copy of runtime.
func CloneRuntime(runtime *config.ProviderInstanceRuntime) *config.ProviderInstanceRuntime {
	if runtime == nil {
		return nil
	}
	clone := *runtime
	if runtime.Streaming != nil {
		streaming := *runtime.Streaming
		clone.Streaming = &streaming
	}
	clone.ExtraBody = CloneAnyMap(runtime.ExtraBody)
	return &clone
}

// CloneStringMap returns a copy of source; nil stays nil.
func CloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// CloneAnyMap returns a shallow copy of source; nil stays nil.
func CloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// RedactedProxyPassword replaces the password of a proxy URL in every view.
// Sending the redacted URL back unchanged keeps the stored proxy.
const RedactedProxyPassword = "****"

// RedactProxyURL returns raw with the password of its userinfo replaced by
// RedactedProxyPassword. URLs without a password come back unchanged.
func RedactProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	schemeEnd := strings.Index(trimmed, "://")
	if schemeEnd < 0 {
		return raw
	}
	authorityStart := schemeEnd + 3
	authority := trimmed[authorityStart:]
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return raw
	}
	user, _, hasPassword := strings.Cut(authority[:at], ":")
	if !hasPassword {
		return raw
	}
	return trimmed[:authorityStart] + user + ":" + RedactedProxyPassword + trimmed[authorityStart+at:]
}

// RedactRuntime returns a copy of runtime safe to show.
func RedactRuntime(runtime *config.ProviderInstanceRuntime) *config.ProviderInstanceRuntime {
	clone := CloneRuntime(runtime)
	if clone != nil {
		clone.Proxy = RedactProxyURL(clone.Proxy)
	}
	return clone
}

// KeepRedactedProxy restores the stored proxy when incoming carries exactly
// the redacted form of it, so a round-tripped form keeps the secret. Any
// other value, including a new password, replaces the stored proxy.
func KeepRedactedProxy(incoming, stored *config.ProviderInstanceRuntime) {
	if incoming == nil || stored == nil {
		return
	}
	proxy := strings.TrimSpace(incoming.Proxy)
	storedProxy := strings.TrimSpace(stored.Proxy)
	if proxy == "" || storedProxy == "" {
		return
	}
	if redacted := RedactProxyURL(storedProxy); redacted != storedProxy && proxy == redacted {
		incoming.Proxy = storedProxy
	}
}

// InstanceIndex returns the index of the instance id in cfg, or -1.
func InstanceIndex(cfg *config.Config, id string) int {
	for i, instance := range cfg.ProviderInstances {
		if instance != nil && instance.ID == id {
			return i
		}
	}
	return -1
}

// RoutesReferencingInstance returns the names of the routes with a target of
// the instance, sorted; never nil.
func RoutesReferencingInstance(routes []*config.ModelRouteConfig, instanceID string) []string {
	references := make([]string, 0)
	for _, route := range routes {
		if route == nil {
			continue
		}
		for _, raw := range route.Targets {
			target, err := config.ParseExactModelTarget(raw)
			if err == nil && target.InstanceID == instanceID {
				references = append(references, route.Name)
				break
			}
		}
	}
	sort.Strings(references)
	return references
}

func referencedErr(id string, routes []string) *InstanceError {
	return &InstanceError{
		Kind:    ErrInstanceConflict,
		Message: fmt.Sprintf("provider instance %q is referenced by routes: %s", id, strings.Join(routes, ", ")),
		Routes:  routes,
	}
}

func loadConfigErr(path string) (*config.Config, error) {
	cfg, err := config.LoadConfig(path)
	if err != nil {
		return nil, instanceErr(ErrInstanceInternal, err, "Failed to load config: %v", err)
	}
	return cfg, nil
}

// InstanceLockPath is the path the instance mutators lock, through
// config.WithFileLock, for their whole load-modify-save of the config, the
// auth store and the catalogs. The OS lock is on its sidecar
// InstanceLockPath()+".flock" in the Compa home, so the launcher and the
// compa-kernel model commands, in other processes, take turns. It is not the
// lock SaveConfig or the catalog store take, so they still work inside it.
func InstanceLockPath() string {
	return filepath.Join(config.GetHome(), "provider-instances")
}

// withInstanceLock runs fn holding the instance lock. A failure to take the
// lock is an internal error; fn's error comes back as it is.
func withInstanceLock(fn func() error) error {
	ran := false
	err := config.WithFileLock(InstanceLockPath(), func() error {
		ran = true
		return fn()
	})
	if err != nil && !ran {
		return instanceErr(ErrInstanceInternal, err, "Failed to lock provider instances: %v", err)
	}
	return err
}

// CreateInstance adds the instance input describes to the config at
// configPath, and stores its API key. A key that fails to store takes the
// instance out of the config again. It holds the instance lock (see
// InstanceLockPath) throughout, so other processes' writes are not lost.
func CreateInstance(configPath string, input InstanceInput, deps InstanceDeps) (*config.ProviderInstanceConfig, error) {
	var created *config.ProviderInstanceConfig
	err := withInstanceLock(func() error {
		var err error
		created, err = createInstanceLocked(configPath, input, deps)
		return err
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func createInstanceLocked(configPath string, input InstanceInput, deps InstanceDeps) (*config.ProviderInstanceConfig, error) {
	deps = deps.withDefaults()
	instance, err := InstanceFromCreateInput(input)
	if err != nil {
		return nil, instanceErr(ErrInstanceInvalid, err, "Validation error: %v", err)
	}
	var pendingCredential *auth.AuthCredential
	credentialKey := InstanceCredentialKey(instance.ID)
	if key := input.apiKey(); key != "" {
		pendingCredential = &auth.AuthCredential{Provider: instance.ProviderKind, AuthMethod: APIKeyAuthMethod, AccessToken: key}
		instance.AuthConnectionRef = "credential:" + credentialKey
	}
	cfg, err := loadConfigErr(configPath)
	if err != nil {
		return nil, err
	}
	if InstanceIndex(cfg, instance.ID) >= 0 {
		return nil, instanceErr(ErrInstanceConflict, nil, "provider instance already exists")
	}
	cfg.ProviderInstances = append(cfg.ProviderInstances, instance)
	if err := cfg.ValidateProviderInstances(); err != nil {
		return nil, instanceErr(ErrInstanceInvalid, err, "Validation error: %v", err)
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return nil, instanceErr(ErrInstanceInternal, err, "Failed to save config: %v", err)
	}
	if pendingCredential != nil {
		if err := deps.SetCredential(credentialKey, pendingCredential); err != nil {
			cfg.ProviderInstances = cfg.ProviderInstances[:len(cfg.ProviderInstances)-1]
			_ = config.SaveConfig(configPath, cfg)
			return nil, instanceErr(ErrInstanceInternal, err, "Failed to save provider credential: %v", err)
		}
	}
	return instance, nil
}

// UpdateInstance updates the instance id as input describes. input.ID must
// be id. A new connection - kind, protocol, endpoint, adapter, credential,
// headers or settings - drops the instance's catalog and its models from the
// active models, the routes and the model selections, as disabling it does;
// disabling an instance routes name is refused. A failure restores the
// config, and the credential it replaced; when the stored credential the
// instance uses cannot be read, nothing changes. It holds the instance lock
// (see InstanceLockPath) throughout.
func UpdateInstance(configPath, id string, input InstanceInput, deps InstanceDeps) (*config.ProviderInstanceConfig, error) {
	var updated *config.ProviderInstanceConfig
	err := withInstanceLock(func() error {
		var err error
		updated, err = updateInstanceLocked(configPath, id, input, deps)
		return err
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func updateInstanceLocked(configPath, id string, input InstanceInput, deps InstanceDeps) (*config.ProviderInstanceConfig, error) {
	deps = deps.withDefaults()
	if input.ID != id {
		return nil, instanceErr(ErrInstanceInvalid, nil, "provider instance id is immutable and must match the request path")
	}
	cfg, err := loadConfigErr(configPath)
	if err != nil {
		return nil, err
	}
	index := InstanceIndex(cfg, id)
	if index < 0 {
		return nil, instanceErr(ErrInstanceNotFound, nil, "provider instance not found")
	}
	previousInstance := cfg.ProviderInstances[index]
	instance, err := InstanceFromUpdateInput(input, previousInstance)
	if err != nil {
		return nil, instanceErr(ErrInstanceInvalid, err, "Validation error: %v", err)
	}
	var pendingCredential, previousCredential *auth.AuthCredential
	// restoreByDelete: with no previous credential, a rollback deletes the
	// new one. It is only safe when the read said there was none.
	restoreByDelete := true
	credentialKey := InstanceCredentialKey(instance.ID)
	if key := input.apiKey(); key != "" {
		var readErr error
		previousCredential, readErr = deps.GetCredential(credentialKey)
		if readErr != nil {
			// The instance uses the stored key: without a copy a rollback
			// could not restore it, so nothing changes.
			if previousInstance.AuthConnectionRef == "credential:"+credentialKey {
				return nil, instanceErr(ErrInstanceInternal, readErr, "Failed to read provider credential: %v", readErr)
			}
			// Nothing names the key; a rollback leaves whatever is stored
			// rather than delete what it could not read.
			previousCredential, restoreByDelete = nil, false
		}
		pendingCredential = &auth.AuthCredential{Provider: instance.ProviderKind, AuthMethod: APIKeyAuthMethod, AccessToken: key}
		instance.AuthConnectionRef = "credential:" + credentialKey
	}
	if instance.State == config.ProviderInstanceStateDisabled {
		if routes := RoutesReferencingInstance(cfg.ModelRoutes, id); len(routes) > 0 {
			return nil, referencedErr(id, routes)
		}
	}
	// A failed update restores the config as it was.
	snapshot, err := loadConfigErr(configPath)
	if err != nil {
		return nil, err
	}
	// A new connection invalidates the instance's catalog. Runtime settings
	// change how requests go, not which models the instance serves, so they
	// keep it.
	connectionChanged := pendingCredential != nil || previousInstance.ProviderKind != instance.ProviderKind ||
		previousInstance.Protocol != instance.Protocol || previousInstance.Endpoint != instance.Endpoint ||
		previousInstance.Adapter != instance.Adapter || previousInstance.AuthConnectionRef != instance.AuthConnectionRef ||
		!reflect.DeepEqual(previousInstance.Headers, instance.Headers) || !reflect.DeepEqual(previousInstance.Settings, instance.Settings)
	if connectionChanged || instance.State == config.ProviderInstanceStateDisabled {
		// No active model, route or model selection may keep naming a model
		// of an instance whose catalog is gone or that is disabled.
		DropTargets(cfg, func(target config.ExactModelTarget) bool { return target.InstanceID != id })
	}
	cfg.ProviderInstances[index] = instance
	if err := cfg.ValidateProviderInstances(); err != nil {
		return nil, instanceErr(ErrInstanceInvalid, err, "Validation error: %v", err)
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return nil, instanceErr(ErrInstanceInternal, err, "Failed to save config: %v", err)
	}
	if pendingCredential != nil {
		if err := deps.SetCredential(credentialKey, pendingCredential); err != nil {
			_ = config.SaveConfig(configPath, snapshot)
			return nil, instanceErr(ErrInstanceInternal, err, "Failed to save provider credential: %v", err)
		}
	}
	if connectionChanged {
		if err := DeleteProviderInstanceCatalog(instance.ID); err != nil {
			if pendingCredential != nil {
				if previousCredential != nil {
					_ = deps.SetCredential(credentialKey, previousCredential)
				} else if restoreByDelete {
					_ = deps.DeleteCredential(credentialKey)
				}
			}
			_ = config.SaveConfig(configPath, snapshot)
			return nil, instanceErr(ErrInstanceInternal, err, "Failed to invalidate provider catalog: %v", err)
		}
	}
	if previousInstance.AuthConnectionRef == "credential:"+credentialKey && instance.AuthConnectionRef != previousInstance.AuthConnectionRef {
		if err := deps.DeleteCredential(credentialKey); err != nil {
			return nil, instanceErr(ErrInstanceInternal, err, "Failed to delete replaced provider credential: %v", err)
		}
	}
	return instance, nil
}

// DeleteInstance removes the instance id, its catalog, its models from the
// active models and the model selections, and the API key stored for it.
// An instance routes name is not removed: the error lists the routes. A
// failure restores the config and the catalogs. It holds the instance lock
// (see InstanceLockPath) throughout.
func DeleteInstance(configPath, id string, deps InstanceDeps) error {
	return withInstanceLock(func() error { return deleteInstanceLocked(configPath, id, deps) })
}

func deleteInstanceLocked(configPath, id string, deps InstanceDeps) error {
	deps = deps.withDefaults()
	cfg, err := loadConfigErr(configPath)
	if err != nil {
		return err
	}
	index := InstanceIndex(cfg, id)
	if index < 0 {
		return instanceErr(ErrInstanceNotFound, nil, "provider instance not found")
	}
	if routes := RoutesReferencingInstance(cfg.ModelRoutes, id); len(routes) > 0 {
		return referencedErr(id, routes)
	}
	// A failed delete restores the config and the catalogs as they were.
	snapshot, err := loadConfigErr(configPath)
	if err != nil {
		return err
	}
	previousCatalogs, err := LoadCatalogs()
	if err != nil {
		return instanceErr(ErrInstanceInternal, err, "Failed to load provider catalogs: %v", err)
	}
	restore := func() {
		_ = config.SaveConfig(configPath, snapshot)
		_ = SaveCatalogs(previousCatalogs)
	}
	removedInstance := cfg.ProviderInstances[index]
	cfg.ProviderInstances = append(cfg.ProviderInstances[:index], cfg.ProviderInstances[index+1:]...)
	DropTargets(cfg, func(target config.ExactModelTarget) bool { return target.InstanceID != id })
	if err := cfg.ValidateProviderInstances(); err != nil {
		return instanceErr(ErrInstanceInvalid, err, "Validation error: %v", err)
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return instanceErr(ErrInstanceInternal, err, "Failed to save config: %v", err)
	}
	if err := DeleteProviderInstanceCatalog(id); err != nil {
		restore()
		return instanceErr(ErrInstanceInternal, err, "Failed to delete provider catalog: %v", err)
	}
	credentialKey := InstanceCredentialKey(id)
	if removedInstance.AuthConnectionRef == "credential:"+credentialKey {
		if err := deps.DeleteCredential(credentialKey); err != nil {
			restore()
			return instanceErr(ErrInstanceInternal, err, "Failed to delete provider credential: %v", err)
		}
	}
	return nil
}

// SyncResult is a synced catalog: the launcher's catalog sync answer.
type SyncResult struct {
	InstanceID string         `json:"instance_id"`
	Models     []CatalogModel `json:"models"`
	Total      int            `json:"total"`
}

// SyncInstanceCatalog fetches the instance id's models through
// deps.SyncCatalog and saves them as its catalog. The models the new catalog
// no longer lists leave the active models, the routes and the model
// selections. A disabled instance, an adapter that cannot list models or an
// instance without an endpoint is refused. The fetch runs without the
// instance lock; the save then takes it, loads the config again and applies
// the catalog to the instance as it is now. An instance removed, made
// ineligible or given a new connection during the fetch is refused.
func SyncInstanceCatalog(ctx context.Context, configPath, id string, deps InstanceDeps) (*SyncResult, error) {
	cfg, err := loadConfigErr(configPath)
	if err != nil {
		return nil, err
	}
	instance, err := syncableInstance(cfg, id)
	if err != nil {
		return nil, err
	}
	if deps.SyncCatalog == nil {
		return nil, instanceErr(ErrInstanceNotConfigured, nil, "provider catalog sync driver is not configured")
	}
	input := CatalogSyncInputFromInstance(instance)
	if strings.TrimSpace(instance.AuthConnectionRef) != "" {
		if deps.ResolveCredential == nil {
			return nil, instanceErr(ErrInstanceInternal, nil, "provider credential resolver is not configured")
		}
		input.Secret, err = deps.ResolveCredential(instance.AuthConnectionRef)
		if err != nil {
			return nil, instanceErr(ErrInstanceUpstream, err, "Failed to resolve provider credential: %v", err)
		}
	}
	models, err := deps.SyncCatalog(ctx, input)
	if err != nil {
		return nil, instanceErr(ErrInstanceUpstream, err, "Failed to sync provider catalog: %v", err)
	}
	fetchedFrom := instance
	err = withInstanceLock(func() error {
		var err error
		instance, err = applySyncedCatalogLocked(configPath, id, fetchedFrom, models)
		return err
	})
	if err != nil {
		return nil, err
	}
	if models == nil {
		models = []CatalogModel{}
	}
	return &SyncResult{InstanceID: instance.ID, Models: models, Total: len(models)}, nil
}

// syncableInstance returns the instance id of cfg when its catalog can sync.
func syncableInstance(cfg *config.Config, id string) (*config.ProviderInstanceConfig, error) {
	index := InstanceIndex(cfg, id)
	if index < 0 {
		return nil, instanceErr(ErrInstanceNotFound, nil, "provider instance not found")
	}
	instance := cfg.ProviderInstances[index]
	if instance.State == config.ProviderInstanceStateDisabled {
		return nil, instanceErr(ErrInstanceDisabled, nil, "provider instance is disabled")
	}
	if !CatalogSyncSupported(instance) {
		return nil, instanceErr(ErrInstanceUnsupported, nil, "provider instance adapter does not support catalog sync")
	}
	if strings.TrimSpace(instance.Endpoint) == "" {
		return nil, instanceErr(ErrInstanceUnsupported, nil, "provider instance endpoint is required for catalog sync")
	}
	return instance, nil
}

// sameConnection reports whether a and b reach the provider the same way, so
// a catalog fetched through a holds for b.
func sameConnection(a, b *config.ProviderInstanceConfig) bool {
	return a.ProviderKind == b.ProviderKind && a.Adapter == b.Adapter && a.Protocol == b.Protocol &&
		a.Endpoint == b.Endpoint && a.AuthConnectionRef == b.AuthConnectionRef &&
		reflect.DeepEqual(a.Headers, b.Headers) && reflect.DeepEqual(a.Settings, b.Settings)
}

// applySyncedCatalogLocked saves models, fetched through fetchedFrom, as the
// catalog of the instance id in the config as it is now, and drops the
// targets the catalog no longer lists. The caller holds the instance lock.
func applySyncedCatalogLocked(configPath, id string, fetchedFrom *config.ProviderInstanceConfig, models []CatalogModel) (*config.ProviderInstanceConfig, error) {
	cfg, err := loadConfigErr(configPath)
	if err != nil {
		return nil, err
	}
	instance, err := syncableInstance(cfg, id)
	if err != nil {
		return nil, err
	}
	if !sameConnection(fetchedFrom, instance) {
		return nil, instanceErr(ErrInstanceConflict, nil, "provider instance changed during catalog sync; sync again")
	}
	previousCatalogs, err := LoadCatalogs()
	if err != nil {
		return nil, instanceErr(ErrInstanceInternal, err, "Failed to load provider catalogs: %v", err)
	}
	if err := SaveProviderInstanceCatalog(instance, models); err != nil {
		return nil, instanceErr(ErrInstanceInternal, err, "Failed to save provider catalog: %v", err)
	}
	available := make(map[string]struct{}, len(models))
	for _, model := range models {
		available[strings.TrimSpace(model.ID)] = struct{}{}
	}
	// The instance's models the new catalog no longer holds leave the active
	// models, the routes and the model selections.
	keep := func(target config.ExactModelTarget) bool {
		if target.InstanceID != instance.ID {
			return true
		}
		_, ok := available[target.ModelID]
		return ok
	}
	if DropsTargets(cfg, keep) {
		DropTargets(cfg, keep)
		if err := config.SaveConfig(configPath, cfg); err != nil {
			_ = SaveCatalogs(previousCatalogs)
			return nil, instanceErr(ErrInstanceInternal, err, "Failed to reconcile provider references: %v", err)
		}
	}
	return instance, nil
}

// DropsTargets reports whether DropTargets(cfg, keep) changes cfg: whether
// keep rejects an exact target among the active models, the routes' targets
// and the model selections. A route, and a selection naming it, only goes
// when one of its targets does.
func DropsTargets(cfg *config.Config, keep func(target config.ExactModelTarget) bool) bool {
	rejected := func(raw string) bool {
		target, err := config.ParseExactModelTarget(raw)
		return err == nil && !keep(target)
	}
	references := append([]string(nil), cfg.ActiveModels...)
	for _, route := range cfg.ModelRoutes {
		if route != nil {
			references = append(references, route.Targets...)
		}
	}
	defaults := cfg.Agents.Defaults
	references = append(references, defaults.ModelName, defaults.ImageModel)
	if defaults.Routing != nil {
		references = append(references, defaults.Routing.LightModel)
	}
	for _, agent := range cfg.Agents.List {
		references = append(references, agent.Model)
	}
	for _, raw := range references {
		if rejected(raw) {
			return true
		}
	}
	return false
}

// InstanceDisplayName is the name to show for an instance: an extension
// instance's provider as the daemon names it, the core registry's label of a
// registry provider kind, else the instance's id.
func InstanceDisplayName(instance *config.ProviderInstanceConfig) string {
	if instance == nil {
		return ""
	}
	if instance.ExtensionProvider() != "" {
		if name, _ := instance.Settings[config.ExtensionDisplayNameSetting].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		return instance.ID
	}
	if entry, ok := coreproviders.RegistryProvider(strings.TrimSpace(instance.ProviderKind)); ok && strings.TrimSpace(entry.Label) != "" {
		return strings.TrimSpace(entry.Label)
	}
	return instance.ID
}

// InstanceView is what can be shown of a provider instance: its credential
// reference, headers and settings are write-only, so only whether a
// credential is configured and the header and setting names show.
type InstanceView struct {
	ID                   string                          `json:"id"`
	DisplayName          string                          `json:"display_name"`
	ProviderKind         string                          `json:"provider_kind"`
	Adapter              string                          `json:"adapter"`
	Protocol             string                          `json:"protocol"`
	Endpoint             string                          `json:"endpoint"`
	State                config.ProviderInstanceState    `json:"state"`
	CredentialConfigured bool                            `json:"credential_configured"`
	HeaderNames          []string                        `json:"header_names"`
	SettingNames         []string                        `json:"setting_names"`
	Runtime              *config.ProviderInstanceRuntime `json:"runtime,omitempty"`
}

// NewInstanceView returns the view of instance.
func NewInstanceView(instance *config.ProviderInstanceConfig) InstanceView {
	return InstanceView{
		ID: instance.ID, DisplayName: InstanceDisplayName(instance), ProviderKind: instance.ProviderKind,
		Adapter: instance.Adapter, Protocol: instance.Protocol, Endpoint: instance.Endpoint, State: instance.State,
		CredentialConfigured: strings.TrimSpace(instance.AuthConnectionRef) != "",
		HeaderNames:          SortedKeys(instance.Headers),
		SettingNames:         SortedKeys(instance.Settings),
		Runtime:              RedactRuntime(instance.Runtime),
	}
}

// ListInstances returns the views of cfg's instances, sorted by id; never nil.
func ListInstances(cfg *config.Config) []InstanceView {
	views := make([]InstanceView, 0, len(cfg.ProviderInstances))
	for _, instance := range cfg.ProviderInstances {
		if instance != nil {
			views = append(views, NewInstanceView(instance))
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}

// SortedKeys returns the keys of m, sorted; never nil.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// InstanceCatalog is the saved catalog of one instance.
type InstanceCatalog struct {
	InstanceID   string         `json:"instance_id"`
	ProviderKind string         `json:"provider_kind"`
	Models       []CatalogModel `json:"models"`
	FetchedAt    string         `json:"fetched_at"`
}

// ProviderTarget is one model of an enabled instance's catalog.
type ProviderTarget struct {
	Target     string `json:"target"`
	InstanceID string `json:"instance_id"`
	ModelID    string `json:"model_id"`
	// Label is the name to show for the model: its catalog display name,
	// else its id.
	Label string `json:"label"`
	// InstanceLabel is the name to show for the target's instance (see
	// InstanceDisplayName).
	InstanceLabel string `json:"instance_label"`
	ProviderKind  string `json:"provider_kind"`
	OwnedBy       string `json:"owned_by,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
	// Surfaces are the llmgw-core surfaces the catalog reports the model
	// serves, such as audio_transcriptions and audio_speech.
	Surfaces  []string `json:"surfaces,omitempty"`
	FetchedAt string   `json:"fetched_at"`
	// AudioInput reports that the catalog declares the model takes audio
	// (see CatalogModel.AudioInput). It is not part of the JSON.
	AudioInput bool `json:"-"`
}

func instancesByID(cfg *config.Config) map[string]*config.ProviderInstanceConfig {
	instances := make(map[string]*config.ProviderInstanceConfig, len(cfg.ProviderInstances))
	for _, instance := range cfg.ProviderInstances {
		if instance != nil {
			instances[instance.ID] = instance
		}
	}
	return instances
}

// ListInstanceCatalogs returns the saved catalogs of cfg's instances, sorted
// by instance and their models by id; never nil.
func ListInstanceCatalogs(cfg *config.Config, store *CatalogStore) []InstanceCatalog {
	instances := instancesByID(cfg)
	catalogs := make([]InstanceCatalog, 0)
	for key, entry := range store.Entries {
		instance := instances[key]
		if !ValidInstanceCatalog(key, entry, instance) {
			continue
		}
		models := append([]CatalogModel{}, entry.Models...)
		sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
		catalogs = append(catalogs, InstanceCatalog{
			InstanceID: instance.ID, ProviderKind: instance.ProviderKind, Models: models, FetchedAt: entry.FetchedAt,
		})
	}
	sort.Slice(catalogs, func(i, j int) bool { return catalogs[i].InstanceID < catalogs[j].InstanceID })
	return catalogs
}

// ListProviderTargets returns every model of the enabled instances' catalogs,
// sorted by target; never nil.
func ListProviderTargets(cfg *config.Config, store *CatalogStore) []ProviderTarget {
	instances := instancesByID(cfg)
	targets := make([]ProviderTarget, 0)
	for key, entry := range store.Entries {
		instance := instances[key]
		if instance == nil || instance.State != config.ProviderInstanceStateEnabled || !ValidInstanceCatalog(key, entry, instance) {
			continue
		}
		instanceLabel := InstanceDisplayName(instance)
		for _, model := range entry.Models {
			modelID := strings.TrimSpace(model.ID)
			if modelID == "" {
				continue
			}
			raw := instance.ID + "/" + modelID
			if _, err := config.ParseExactModelTarget(raw); err != nil {
				continue
			}
			label := strings.TrimSpace(model.DisplayName)
			if label == "" {
				label = modelID
			}
			targets = append(targets, ProviderTarget{
				Target: raw, InstanceID: instance.ID, ModelID: modelID,
				Label: label, InstanceLabel: instanceLabel,
				ProviderKind: instance.ProviderKind, OwnedBy: model.OwnedBy, DisplayName: model.DisplayName,
				Surfaces: model.Surfaces, FetchedAt: entry.FetchedAt, AudioInput: model.AudioInput,
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Target < targets[j].Target })
	return targets
}

// ChatShortlistTargets returns the targets of all that are on cfg's chat
// shortlist (the active models) and serve chat; never nil. Chat offers these.
func ChatShortlistTargets(cfg *config.Config, all []ProviderTarget) []ProviderTarget {
	filtered := make([]ProviderTarget, 0, len(cfg.ActiveModels))
	if len(cfg.ActiveModels) == 0 {
		return filtered
	}
	activeSet := make(map[string]bool, len(cfg.ActiveModels))
	for _, m := range cfg.ActiveModels {
		activeSet[strings.TrimSpace(m)] = true
	}
	for _, target := range all {
		// A speech-only model never reaches the Chat selector, even when
		// shortlisted.
		if activeSet[target.Target] && ServesChat(target.Surfaces) {
			filtered = append(filtered, target)
		}
	}
	return filtered
}
