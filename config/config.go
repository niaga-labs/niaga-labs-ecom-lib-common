package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config represents application configuration
type Config struct {
	App      AppConfig      `mapstructure:"app"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	JWT      JWTConfig      `mapstructure:"jwt"`
}

type AppConfig struct {
	Name string `mapstructure:"name"`
	Env  string `mapstructure:"env"`
	Port string `mapstructure:"port"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     string `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	Name     string `mapstructure:"name"`
	SSLMode  string `mapstructure:"ssl_mode"`
}

type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     string `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type JWTConfig struct {
	Secret             string `mapstructure:"secret"`
	AccessTokenExpiry  string `mapstructure:"access_token_expiry"`
	RefreshTokenExpiry string `mapstructure:"refresh_token_expiry"`
}

// LoadConfig loads configuration from environment variables and config file
func LoadConfig(configPath string) (*Config, error) {
	v := viper.New()

	// Set config file
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
	}

	// Environment variables
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Set default values
	setDefaults(v)

	// Read config file (optional)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found, use defaults and env vars
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

func setDefaults(v *viper.Viper) {
	// App
	v.SetDefault("app.env", "development")
	v.SetDefault("app.port", "8080")

	// Database
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", "5432")
	v.SetDefault("database.ssl_mode", "disable")

	// Redis
	v.SetDefault("redis.host", "localhost")
	v.SetDefault("redis.port", "6379")
	v.SetDefault("redis.db", 0)

	// JWT
	v.SetDefault("jwt.access_token_expiry", "15m")
	v.SetDefault("jwt.refresh_token_expiry", "168h")
}
