package config

// ServiceConfig holds common service configuration
type ServiceConfig struct {
	Name        string `yaml:"name" env:"SERVICE_NAME"`
	Environment string `yaml:"environment" env:"ENVIRONMENT" env-default:"development"`
	Debug       bool   `yaml:"debug" env:"DEBUG" env-default:"false"`
}

// IsProduction returns true if environment is production
func (c ServiceConfig) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "prod"
}

// IsDevelopment returns true if environment is development
func (c ServiceConfig) IsDevelopment() bool {
	return c.Environment == "development" || c.Environment == "dev"
}
