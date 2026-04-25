package provider

// Provider defines the interface for cloud providers.
type Provider interface {
	Apply(vars map[string]string) error
	Destroy() error
	Status() (string, error)
	GenerateTFVars() (map[string]string, error)
}
