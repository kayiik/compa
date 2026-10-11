import type { ChannelConfig } from "@/api/channels"

export const SECRET_FIELD_MAP = {
  token: "_token",
  bot_token: "_bot_token",
  app_token: "_app_token",
} as const

const CHANNEL_SECRET_FIELDS: Record<string, string[]> = {
  slack: ["bot_token", "app_token"],
  web: ["token"],
}

const SECRET_FIELD_SET = new Set(Object.keys(SECRET_FIELD_MAP))

function asString(value: unknown): string {
  return typeof value === "string" ? value : ""
}

export function isSecretField(key: string): boolean {
  return SECRET_FIELD_SET.has(key)
}

export function buildEditConfig(
  channelName: string,
  config: ChannelConfig,
): ChannelConfig {
  const edit: ChannelConfig = { ...config }

  for (const key of CHANNEL_SECRET_FIELDS[channelName] ?? []) {
    if (!(key in edit)) {
      edit[key] = ""
    }
    const editKey = SECRET_FIELD_MAP[key as keyof typeof SECRET_FIELD_MAP]
    if (editKey) {
      edit[editKey] = ""
    }
  }

  return edit
}

export function hasConfiguredSecret(
  configuredSecrets: readonly string[],
  key: string,
): boolean {
  return configuredSecrets.includes(key)
}

export function getFieldValueForValidation(
  config: ChannelConfig,
  configuredSecrets: readonly string[],
  key: string,
): unknown {
  const editKey = SECRET_FIELD_MAP[key as keyof typeof SECRET_FIELD_MAP]
  if (editKey) {
    const incoming = asString(config[editKey]).trim()
    if (incoming !== "") {
      return incoming
    }
    if (hasConfiguredSecret(configuredSecrets, key)) {
      return true
    }
  }
  return config[key]
}

export function getSecretInputPlaceholder(
  configuredSecrets: readonly string[],
  key: string,
  configuredPlaceholder: string,
  fallback = "",
): string {
  return hasConfiguredSecret(configuredSecrets, key)
    ? configuredPlaceholder
    : fallback
}

function allowEntries(config: ChannelConfig): string[] {
  const entries = Array.isArray(config.allow_from) ? config.allow_from : []
  return entries
    .filter((entry): entry is string => typeof entry === "string")
    .map((entry) => entry.trim())
    .filter((entry) => entry !== "")
}

/**
 * Whether allow_from lists the owner's account: an entry other than "*",
 * which admits no one. Until it does, the gateway records the sender of a
 * direct message as a pairing request, for the owner to approve.
 */
export function hasOwnerAccount(config: ChannelConfig): boolean {
  return allowEntries(config).some((entry) => entry !== "*")
}
