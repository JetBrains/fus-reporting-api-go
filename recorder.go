package fus

type RecorderConfig struct {
	// FUS recorder code (e.g., "TC").
	RecorderID string

	// Recorder protocol version.
	RecorderVersion int

	// Product identifier (e.g., "TCC" for TeamCity CLI).
	ProductCode string

	// Product version string (e.g., "0.5.0").
	BuildVersion string

	// Whether this is a JetBrains internal user.
	Internal bool

	// Directory for persisting device ID, buffer, and config cache.
	DataDir string

	// Overrides automatic device ID generation. If empty, one is generated.
	DeviceID string

	// CDN region for config endpoints. Defaults to RegionAll.
	Region RegionCode

	// AnonymizationSalt is the product-supplied salt used for SHA-256 device/session anonymization. Mirrors JVM FusClientConfig.anonymizationSalt. Takes priority over any salt in the public config.
	AnonymizationSalt string
}
