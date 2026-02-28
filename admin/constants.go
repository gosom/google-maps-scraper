package admin

//nolint:gosec // These are configuration key names, not credentials.
const (
	DatabaseURLKey              = "database_url"
	RegistryProviderSettingsKey = "registry_provider_settings"
	HashSaltKey                 = "hash_salt"
	AppKeyPairKey               = "app_key_pair"
	DOTokenKey                  = "do_api_token"
	HetznerTokenKey             = "hetzner_api_token"
)
