package model

// The provider instance commands: list, add, update and remove instances,
// list their catalogs and models, and sync a catalog. They share
// modelservice's instance operations with the launcher's
// /api/provider-instances routes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/kayiik/compa/cmd/compa-kernel/internal"
	"github.com/kayiik/compa/cmd/compa-kernel/internal/apikey"
	"github.com/kayiik/compa/cmd/compa-kernel/internal/jsonout"
	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/gatewayreload"
	"github.com/kayiik/compa/pkg/modelservice"
)

// syncTimeout bounds a catalog sync.
const syncTimeout = 60 * time.Second

// Seams tests replace: the effects of the instance operations, the running
// gateway's reload, and where and how the API key is read.
var (
	instanceDeps  = modelservice.DefaultInstanceDeps
	reloadGateway = func(ctx context.Context, configPath string) (bool, error) {
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return false, err
		}
		return gatewayreload.Reload(ctx, config.GetHome(), cfg)
	}
	keyInput    = func() *os.File { return os.Stdin }
	keyTerminal = apikey.System
)

// instanceList is what `model instances --json` prints.
type instanceList struct {
	Instances []modelservice.InstanceView `json:"instances"`
	Total     int                         `json:"total"`
}

// instanceChange is what `model add` and `model update` print: the
// instance as saved, and whether the running gateway reloaded the config.
type instanceChange struct {
	Status   string                    `json:"status"`
	Instance modelservice.InstanceView `json:"instance"`
	Reloaded bool                      `json:"reloaded"`
}

// instanceRemoval is what `model remove` prints.
type instanceRemoval struct {
	Status     string `json:"status"`
	InstanceID string `json:"instance_id"`
	Reloaded   bool   `json:"reloaded"`
}

// catalogSync is what `model sync` prints: the launcher's catalog sync
// answer, and whether the running gateway reloaded the config.
type catalogSync struct {
	modelservice.SyncResult
	Reloaded bool `json:"reloaded"`
}

type catalogList struct {
	Catalogs []modelservice.InstanceCatalog `json:"catalogs"`
	Total    int                            `json:"total"`
}

type targetList struct {
	Targets []modelservice.ProviderTarget `json:"targets"`
	Total   int                           `json:"total"`
}

// instanceCommandError is a failed instance operation. Its JSON document
// carries the routes that block it, if any.
type instanceCommandError struct {
	err *modelservice.InstanceError
	msg string
}

func (e *instanceCommandError) Error() string { return e.msg }
func (e *instanceCommandError) Unwrap() error { return e.err }

func (e *instanceCommandError) JSONDetails() map[string]any {
	if len(e.err.Routes) == 0 {
		return nil
	}
	return map[string]any{"routes": e.err.Routes}
}

// commandError words an instance operation's error for the command line.
func commandError(id string, err error) error {
	var instanceErr *modelservice.InstanceError
	if !errors.As(err, &instanceErr) {
		return err
	}
	msg := instanceErr.Message
	if errors.Is(err, modelservice.ErrInstanceNotFound) {
		msg = fmt.Sprintf("provider instance %q not found", id)
	}
	return &instanceCommandError{err: instanceErr, msg: msg}
}

func newInstancesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "instances",
		Short: "List the provider instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			views := modelservice.ListInstances(cfg)
			if jsonout.Requested(cmd) {
				return jsonout.Write(cmd.OutOrStdout(), instanceList{Instances: views, Total: len(views)})
			}
			out := cmd.OutOrStdout()
			if len(views) == 0 {
				fmt.Fprintln(out, "No provider instances configured.")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tKIND\tADAPTER\tSTATE\tCREDENTIAL\tENDPOINT")
			for _, view := range views {
				credential := "-"
				if view.CredentialConfigured {
					credential = "configured"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", view.ID, view.ProviderKind, view.Adapter, view.State, credential, view.Endpoint)
			}
			return w.Flush()
		},
	}
}

// instanceFlags are the flags of `model add` and `model update`.
type instanceFlags struct {
	kind, protocol, endpoint, adapter string
	headers, settings                 []string
	disabled, apiKeyStdin             bool
	clearAPIKey, clearHeaders         bool
	clearSettings                     bool
}

func (f *instanceFlags) register(cmd *cobra.Command, update bool) {
	flags := cmd.Flags()
	flags.StringVar(&f.kind, "kind", "", "Provider kind, such as openai or ollama (add: default the id)")
	flags.StringVar(&f.protocol, "protocol", "", "Protocol, such as openai or anthropic (add: default the provider's)")
	flags.StringVar(&f.endpoint, "endpoint", "", "Endpoint URL (add: default the provider's)")
	flags.StringVar(&f.adapter, "adapter", "", "Adapter: openai-compatible, anthropic-compatible, native or extension (add: default the provider's)")
	flags.StringArrayVar(&f.headers, "header", nil, "Request header name=value (repeatable)")
	flags.StringArrayVar(&f.settings, "setting", nil, "Adapter setting name=value, the value JSON when it parses as JSON (repeatable)")
	flags.BoolVar(&f.disabled, "disabled", false, "Disable the instance (update: --disabled=false enables it)")
	flags.BoolVar(&f.apiKeyStdin, "api-key-stdin", false, "Read the instance's API key from stdin")
	if update {
		flags.BoolVar(&f.clearAPIKey, "clear-api-key", false, "Remove the instance's credential")
		flags.BoolVar(&f.clearHeaders, "clear-headers", false, "Remove the instance's headers (with --header: replace them)")
		flags.BoolVar(&f.clearSettings, "clear-settings", false, "Remove the instance's settings (with --setting: replace them)")
	}
}

func parsePairs(pairs []string, what string) (map[string]string, error) {
	values := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("--%s %q: want name=value", what, pair)
		}
		values[strings.TrimSpace(name)] = value
	}
	return values, nil
}

func parseSettings(pairs []string) (map[string]any, error) {
	raw, err := parsePairs(pairs, "setting")
	if err != nil {
		return nil, err
	}
	settings := make(map[string]any, len(raw))
	for name, value := range raw {
		var parsed any
		if json.Unmarshal([]byte(value), &parsed) == nil {
			settings[name] = parsed
		} else {
			settings[name] = value
		}
	}
	return settings, nil
}

// readKey reads the API key from stdin, prompting on the progress writer.
func readKey(cmd *cobra.Command, id string) (*string, error) {
	key, err := apikey.Read(id, keyInput(), jsonout.Progress(cmd), keyTerminal)
	if err != nil {
		return nil, fmt.Errorf("reading the API key: %w", err)
	}
	return &key, nil
}

func newAddCommand() *cobra.Command {
	var f instanceFlags
	cmd := &cobra.Command{
		Use:   "add <instance-id>",
		Short: "Add a provider instance",
		Long: `Add a provider instance. The API key, if any, is read from stdin with
--api-key-stdin; it is never an argument.

Examples:
  compa-kernel model add ollama --endpoint http://127.0.0.1:11434/v1
  echo "$KEY" | compa-kernel model add work --kind openai --api-key-stdin`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			configPath := internal.GetConfigPath()
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			input := modelservice.InstanceInput{
				ID: id, ProviderKind: f.kind, Adapter: f.adapter, Protocol: f.protocol, Endpoint: f.endpoint,
				State: config.ProviderInstanceStateEnabled,
			}
			if input.ProviderKind == "" {
				input.ProviderKind = id
			}
			applyRosterDefaults(cfg, &input)
			if f.disabled {
				input.State = config.ProviderInstanceStateDisabled
			}
			if len(f.headers) > 0 {
				headers, err := parsePairs(f.headers, "header")
				if err != nil {
					return err
				}
				input.Headers = &headers
			}
			if len(f.settings) > 0 {
				settings, err := parseSettings(f.settings)
				if err != nil {
					return err
				}
				input.Settings = &settings
			}
			if f.apiKeyStdin {
				if input.APIKey, err = readKey(cmd, id); err != nil {
					return err
				}
			}
			instance, err := modelservice.CreateInstance(configPath, input, instanceDeps())
			if err != nil {
				return commandError(id, err)
			}
			return reportInstanceChange(cmd, configPath, "Added", instance)
		},
	}
	f.register(cmd, false)
	return cmd
}

// applyRosterDefaults fills the adapter, protocol and endpoint input leaves
// empty from the curated provider of its kind, when there is one.
func applyRosterDefaults(cfg *config.Config, input *modelservice.InstanceInput) {
	for _, item := range modelservice.ListRoster(cfg) {
		if item.ID != input.ProviderKind {
			continue
		}
		if input.Adapter == "" {
			input.Adapter = item.Adapter
		}
		if input.Protocol == "" {
			input.Protocol = item.Protocol
		}
		if input.Endpoint == "" {
			input.Endpoint = item.DefaultEndpoint
		}
		return
	}
}

func newUpdateCommand() *cobra.Command {
	var f instanceFlags
	cmd := &cobra.Command{
		Use:   "update <instance-id>",
		Short: "Change a provider instance; only the flags given change",
		Long: `Change a provider instance; only the flags given change. --header and
--setting add to the instance's headers and settings unless --clear-headers or
--clear-settings is given too. A new connection drops the instance's catalog:
run "model sync" afterwards. The API key is read from stdin with
--api-key-stdin; it is never an argument.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			configPath := internal.GetConfigPath()
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			index := modelservice.InstanceIndex(cfg, id)
			if index < 0 {
				return fmt.Errorf("provider instance %q not found", id)
			}
			if f.apiKeyStdin && f.clearAPIKey {
				return fmt.Errorf("use either --api-key-stdin or --clear-api-key, not both")
			}
			existing := cfg.ProviderInstances[index]
			input := modelservice.InputFromInstance(existing)
			flags := cmd.Flags()
			if flags.Changed("kind") {
				input.ProviderKind = f.kind
			}
			if flags.Changed("protocol") {
				input.Protocol = f.protocol
			}
			if flags.Changed("endpoint") {
				input.Endpoint = f.endpoint
			}
			if flags.Changed("adapter") {
				input.Adapter = f.adapter
			}
			if flags.Changed("disabled") {
				input.State = config.ProviderInstanceStateEnabled
				if f.disabled {
					input.State = config.ProviderInstanceStateDisabled
				}
			}
			if len(f.headers) > 0 {
				given, err := parsePairs(f.headers, "header")
				if err != nil {
					return err
				}
				headers := map[string]string{}
				if !f.clearHeaders {
					headers = modelservice.CloneStringMap(existing.Headers)
					if headers == nil {
						headers = map[string]string{}
					}
				}
				for name, value := range given {
					headers[name] = value
				}
				input.Headers = &headers
			} else {
				input.ClearHeaders = f.clearHeaders
			}
			if len(f.settings) > 0 {
				given, err := parseSettings(f.settings)
				if err != nil {
					return err
				}
				settings := map[string]any{}
				if !f.clearSettings {
					settings = modelservice.CloneAnyMap(existing.Settings)
					if settings == nil {
						settings = map[string]any{}
					}
				}
				for name, value := range given {
					settings[name] = value
				}
				input.Settings = &settings
			} else {
				input.ClearSettings = f.clearSettings
			}
			input.ClearAuth = f.clearAPIKey
			if f.apiKeyStdin {
				if input.APIKey, err = readKey(cmd, id); err != nil {
					return err
				}
			}
			instance, err := modelservice.UpdateInstance(configPath, id, input, instanceDeps())
			if err != nil {
				return commandError(id, err)
			}
			return reportInstanceChange(cmd, configPath, "Updated", instance)
		},
	}
	f.register(cmd, true)
	return cmd
}

func reportInstanceChange(cmd *cobra.Command, configPath, verb string, instance *config.ProviderInstanceConfig) error {
	reloaded := reload(cmd, configPath)
	view := modelservice.NewInstanceView(instance)
	if jsonout.Requested(cmd) {
		return jsonout.Write(cmd.OutOrStdout(), instanceChange{Status: "ok", Instance: view, Reloaded: reloaded})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "V %s provider instance %s (%s, %s)\n", verb, view.ID, view.ProviderKind, view.State)
	if view.State == config.ProviderInstanceStateEnabled {
		fmt.Fprintf(out, "  Run \"compa-kernel model sync %s\" to list its models.\n", view.ID)
	}
	return nil
}

// reload asks the running gateway to reload the config, best effort, and
// reports whether it did. Text output says what to do when it did not.
func reload(cmd *cobra.Command, configPath string) bool {
	reloaded, err := reloadGateway(cmd.Context(), configPath)
	progress := jsonout.Progress(cmd)
	switch {
	case err != nil:
		fmt.Fprintf(progress, "The running gateway did not reload (%v); restart it to apply the change.\n", err)
	case reloaded:
		if !jsonout.Requested(cmd) {
			fmt.Fprintln(progress, "The running gateway reloaded its config.")
		}
	default:
		if !jsonout.Requested(cmd) {
			fmt.Fprintln(progress, "No running gateway reached; the change applies when it starts.")
		}
	}
	return reloaded
}

func newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <instance-id>",
		Short: "Remove a provider instance, its catalog and its stored API key",
		Long: `Remove a provider instance, its catalog and its stored API key. Its models
leave the chat shortlist and the model selections. An instance a model route
names is not removed; the error lists the routes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			configPath := internal.GetConfigPath()
			if err := modelservice.DeleteInstance(configPath, id, instanceDeps()); err != nil {
				return commandError(id, err)
			}
			reloaded := reload(cmd, configPath)
			if jsonout.Requested(cmd) {
				return jsonout.Write(cmd.OutOrStdout(), instanceRemoval{Status: "ok", InstanceID: id, Reloaded: reloaded})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "V Removed provider instance %s\n", id)
			return nil
		},
	}
}

func newCatalogsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "catalogs",
		Short: "List the saved catalogs of the provider instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, err := loadProjection()
			if err != nil {
				return err
			}
			catalogs := modelservice.ListInstanceCatalogs(cfg, store)
			if jsonout.Requested(cmd) {
				return jsonout.Write(cmd.OutOrStdout(), catalogList{Catalogs: catalogs, Total: len(catalogs)})
			}
			out := cmd.OutOrStdout()
			if len(catalogs) == 0 {
				fmt.Fprintln(out, "No saved catalogs. Run \"compa-kernel model sync <instance-id>\".")
				return nil
			}
			for _, catalog := range catalogs {
				fmt.Fprintf(out, "%s (%s): %d models, fetched %s\n", catalog.InstanceID, catalog.ProviderKind, len(catalog.Models), catalog.FetchedAt)
			}
			return nil
		},
	}
}

func newModelsCommand() *cobra.Command {
	var all bool
	var instanceID string
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List the models chat offers: the shortlisted ones, or with --all every model",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, store, err := loadProjection()
			if err != nil {
				return err
			}
			targets := modelservice.ListProviderTargets(cfg, store)
			if !all {
				targets = modelservice.ChatShortlistTargets(cfg, targets)
			}
			if instanceID = strings.TrimSpace(instanceID); instanceID != "" {
				filtered := make([]modelservice.ProviderTarget, 0, len(targets))
				for _, target := range targets {
					if target.InstanceID == instanceID {
						filtered = append(filtered, target)
					}
				}
				targets = filtered
			}
			if jsonout.Requested(cmd) {
				return jsonout.Write(cmd.OutOrStdout(), targetList{Targets: targets, Total: len(targets)})
			}
			return printTargets(cmd.OutOrStdout(), targets, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "List every model of the enabled instances' catalogs")
	cmd.Flags().StringVar(&instanceID, "instance", "", "Only the models of this instance")
	return cmd
}

func printTargets(out io.Writer, targets []modelservice.ProviderTarget, all bool) error {
	if len(targets) == 0 {
		if all {
			fmt.Fprintln(out, "No models. Run \"compa-kernel model sync <instance-id>\".")
		} else {
			fmt.Fprintln(out, "No models on the chat shortlist. Use --all to list every model.")
		}
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TARGET\tLABEL")
	for _, target := range targets {
		fmt.Fprintf(w, "%s\t%s\n", target.Target, target.Label)
	}
	return w.Flush()
}

func loadProjection() (*config.Config, *modelservice.CatalogStore, error) {
	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	store, err := modelservice.LoadCatalogs()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load catalogs: %w", err)
	}
	return cfg, store, nil
}

func newSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync <instance-id>",
		Short: "Fetch a provider instance's models and save them as its catalog",
		Long: `Fetch a provider instance's models and save them as its catalog. Models the
new catalog no longer lists leave the chat shortlist, the routes and the model
selections.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			configPath := internal.GetConfigPath()
			ctx, cancel := context.WithTimeout(cmd.Context(), syncTimeout)
			defer cancel()
			fmt.Fprintf(jsonout.Progress(cmd), "Fetching the models of %s...\n", id)
			result, err := modelservice.SyncInstanceCatalog(ctx, configPath, id, instanceDeps())
			if err != nil {
				return commandError(id, err)
			}
			reloaded := reload(cmd, configPath)
			if jsonout.Requested(cmd) {
				return jsonout.Write(cmd.OutOrStdout(), catalogSync{SyncResult: *result, Reloaded: reloaded})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "V %s: %d models\n", result.InstanceID, result.Total)
			return nil
		},
	}
}
