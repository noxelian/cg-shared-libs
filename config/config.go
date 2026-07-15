package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load loads configuration from yaml file and environment variables
func Load[T any](path string) (*T, error) {
	var cfg T

	// Load from YAML file if exists
	if path != "" {
		// #nosec G304 -- The caller intentionally selects the service configuration file.
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("read config file: %w", err)
			}
		} else {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("parse config file: %w", err)
			}
		}
	}

	// Override with environment variables
	if err := loadFromEnv(&cfg); err != nil {
		return nil, fmt.Errorf("load env vars: %w", err)
	}

	return &cfg, nil
}

// MustLoad loads configuration or panics
func MustLoad[T any](path string) *T {
	cfg, err := Load[T](path)
	if err != nil {
		panic(err)
	}
	return cfg
}

func loadFromEnv(cfg any) error {
	return processStruct(reflect.ValueOf(cfg).Elem())
}

func processStruct(v reflect.Value) error {
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Handle nested structs
		if field.Kind() == reflect.Struct {
			if err := processStruct(field); err != nil {
				return err
			}
			continue
		}

		// Get env tag
		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			continue
		}

		// Get default value
		defaultVal := fieldType.Tag.Get("env-default")

		// Get value from environment
		value := os.Getenv(envTag)
		if value == "" {
			value = defaultVal
		}

		if value == "" {
			continue
		}

		// Set field value
		if err := setField(field, value); err != nil {
			return fmt.Errorf("set field %s: %w", fieldType.Name, err)
		}
	}

	return nil
}

func setField(field reflect.Value, value string) error {
	if !field.CanSet() {
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(value)
			if err != nil {
				return err
			}
			field.SetInt(int64(d))
		} else {
			i, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			field.SetInt(i)
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		i, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		field.SetUint(i)

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		field.SetFloat(f)

	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(b)

	case reflect.Slice:
		if field.Type().Elem().Kind() == reflect.String {
			parts := strings.Split(value, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			field.Set(reflect.ValueOf(parts))
		}
	}

	return nil
}

// GetEnv returns environment variable value or default
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetEnvInt returns environment variable as int or default
func GetEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

// GetEnvBool returns environment variable as bool or default
func GetEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

// GetEnvDuration returns environment variable as duration or default
func GetEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
