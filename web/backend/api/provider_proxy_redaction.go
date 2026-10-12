package api

import (
	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/modelservice"
)

// redactProxyURL returns raw with the password of its userinfo replaced by
// modelservice.RedactedProxyPassword. URLs without a password come back unchanged.
func redactProxyURL(raw string) string { return modelservice.RedactProxyURL(raw) }

// redactProviderInstanceRuntime returns a copy of runtime safe to show.
func redactProviderInstanceRuntime(runtime *config.ProviderInstanceRuntime) *config.ProviderInstanceRuntime {
	return modelservice.RedactRuntime(runtime)
}

// keepRedactedProxy restores the stored proxy when incoming carries exactly the
// redacted form of it (see modelservice.KeepRedactedProxy).
func keepRedactedProxy(incoming, stored *config.ProviderInstanceRuntime) {
	modelservice.KeepRedactedProxy(incoming, stored)
}

// keepRedactedInstanceProxies applies keepRedactedProxy to every incoming
// instance that already exists in stored, matched by ID.
func keepRedactedInstanceProxies(incoming, stored []*config.ProviderInstanceConfig) {
	byID := make(map[string]*config.ProviderInstanceConfig, len(stored))
	for _, instance := range stored {
		if instance != nil {
			byID[instance.ID] = instance
		}
	}
	for _, instance := range incoming {
		if instance == nil {
			continue
		}
		if previous := byID[instance.ID]; previous != nil {
			keepRedactedProxy(instance.Runtime, previous.Runtime)
		}
	}
}

// redactInstanceProxies redacts the proxy password of every instance in place.
func redactInstanceProxies(instances []*config.ProviderInstanceConfig) {
	for _, instance := range instances {
		if instance != nil && instance.Runtime != nil {
			instance.Runtime.Proxy = redactProxyURL(instance.Runtime.Proxy)
		}
	}
}
