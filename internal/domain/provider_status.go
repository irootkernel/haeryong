package domain

type ProviderStatus struct {
	InputFormats         []string `json:"input_formats"`
	NativeInputTelemetry bool     `json:"native_input_telemetry"`
	OutputFormat         string   `json:"output_format"`
	Provider             string   `json:"provider_id"`
	Reason               string   `json:"reason,omitempty"`
	State                string   `json:"state"`
	Version              string   `json:"version,omitempty"`
	Authentication       string   `json:"authentication_mode"`
	Model                string   `json:"model,omitempty"`
	Effort               string   `json:"effort,omitempty"`
	OrdinaryUsageAllowed *bool    `json:"ordinary_usage_allowed"`
	ReadOnly             bool     `json:"read_only"`
	Lookup               bool     `json:"lookup_supported"`
	Cancellation         bool     `json:"cancellation_supported"`
	Models               []string `json:"models"`
	// A fresh native catalog is availability information, never entitlement proof.
	CatalogProvesAccess bool `json:"catalog_proves_access"`
}
