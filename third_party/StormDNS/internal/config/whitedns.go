// WhiteDNS integration: use upstream defaults without config files.
package config

func EmbeddedDefaults() ServerConfig { return defaultServerConfig() }
