package validation

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidator_ValidateMaxLength(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		maxLen   int
		hasError bool
	}{
		{
			name:     "within limit",
			field:    "name",
			value:    "John",
			maxLen:   255,
			hasError: false,
		},
		{
			name:     "at limit",
			field:    "name",
			value:    strings.Repeat("a", 255),
			maxLen:   255,
			hasError: false,
		},
		{
			name:     "exceeds limit",
			field:    "name",
			value:    strings.Repeat("a", 256),
			maxLen:   255,
			hasError: true,
		},
		{
			name:     "empty string",
			field:    "name",
			value:    "",
			maxLen:   255,
			hasError: false,
		},
		{
			name:     "unicode characters count correctly",
			field:    "name",
			value:    "Иван Петров", // 11 characters
			maxLen:   10,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateMaxLength(tt.field, tt.value, tt.maxLen)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidateRequired(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		hasError bool
	}{
		{
			name:     "non-empty",
			field:    "name",
			value:    "John",
			hasError: false,
		},
		{
			name:     "empty",
			field:    "name",
			value:    "",
			hasError: true,
		},
		{
			name:     "whitespace only",
			field:    "name",
			value:    "   ",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateRequired(tt.field, tt.value)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidatePhone(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		required bool
		hasError bool
	}{
		{
			name:     "valid phone with +",
			field:    "phone",
			value:    "+79991234567",
			required: true,
			hasError: false,
		},
		{
			name:     "valid phone without +",
			field:    "phone",
			value:    "79991234567",
			required: true,
			hasError: false,
		},
		{
			name:     "too short",
			field:    "phone",
			value:    "123456",
			required: true,
			hasError: true,
		},
		{
			name:     "too long",
			field:    "phone",
			value:    "+1234567890123456",
			required: true,
			hasError: true,
		},
		{
			name:     "empty optional",
			field:    "phone",
			value:    "",
			required: false,
			hasError: false,
		},
		{
			name:     "empty required",
			field:    "phone",
			value:    "",
			required: true,
			hasError: true,
		},
		{
			name:     "with letters",
			field:    "phone",
			value:    "+7999abc1234",
			required: true,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidatePhone(tt.field, tt.value, tt.required)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidateEmail(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		required bool
		hasError bool
	}{
		{
			name:     "valid email",
			field:    "email",
			value:    "test@example.com",
			required: true,
			hasError: false,
		},
		{
			name:     "invalid email - no @",
			field:    "email",
			value:    "testexample.com",
			required: true,
			hasError: true,
		},
		{
			name:     "invalid email - no domain",
			field:    "email",
			value:    "test@",
			required: true,
			hasError: true,
		},
		{
			name:     "empty optional",
			field:    "email",
			value:    "",
			required: false,
			hasError: false,
		},
		{
			name:     "empty required",
			field:    "email",
			value:    "",
			required: true,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateEmail(tt.field, tt.value, tt.required)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidateUUID(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		required bool
		hasError bool
	}{
		{
			name:     "valid UUID",
			field:    "id",
			value:    "550e8400-e29b-41d4-a716-446655440000",
			required: true,
			hasError: false,
		},
		{
			name:     "invalid UUID - wrong format",
			field:    "id",
			value:    "not-a-uuid",
			required: true,
			hasError: true,
		},
		{
			name:     "invalid UUID - missing segment",
			field:    "id",
			value:    "550e8400-e29b-41d4-a716",
			required: true,
			hasError: true,
		},
		{
			name:     "empty optional",
			field:    "id",
			value:    "",
			required: false,
			hasError: false,
		},
		{
			name:     "empty required",
			field:    "id",
			value:    "",
			required: true,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateUUID(tt.field, tt.value, tt.required)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidateArrayLength(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		length   int
		maxLen   int
		hasError bool
	}{
		{
			name:     "within limit",
			field:    "items",
			length:   10,
			maxLen:   50,
			hasError: false,
		},
		{
			name:     "at limit",
			field:    "items",
			length:   50,
			maxLen:   50,
			hasError: false,
		},
		{
			name:     "exceeds limit",
			field:    "items",
			length:   51,
			maxLen:   50,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateArrayLength(tt.field, tt.length, tt.maxLen)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_Chaining(t *testing.T) {
	v := NewValidator()
	v.ValidateName("name", "John", true).
		ValidatePhone("phone", "+79991234567", true).
		ValidateEmail("email", "test@example.com", false).
		ValidateDescription("description", "Some description")

	assert.False(t, v.HasErrors())
}

func TestValidator_MultipleErrors(t *testing.T) {
	v := NewValidator()
	v.ValidateName("name", "", true).            // Error: required
		ValidatePhone("phone", "invalid", true). // Error: format
		ValidateEmail("email", "invalid", true)  // Error: format

	require.True(t, v.HasErrors())
	assert.Len(t, v.Errors(), 3)
}

func TestValidator_ErrorString(t *testing.T) {
	v := NewValidator()
	v.ValidateName("name", "", true)

	errStr := v.ErrorString()
	assert.Contains(t, errStr, "name")
	assert.Contains(t, errStr, "required")
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		Field:   "name",
		Message: "is required",
		Err:     ErrFieldRequired,
	}

	assert.Equal(t, "name: is required", err.Error())
	assert.True(t, errors.Is(err, ErrFieldRequired))
}

func TestValidateStringMaxLength(t *testing.T) {
	err := ValidateStringMaxLength("name", "John", 255)
	assert.NoError(t, err)

	err = ValidateStringMaxLength("name", strings.Repeat("a", 256), 255)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrFieldTooLong))
}

func TestValidateStringRequired(t *testing.T) {
	err := ValidateStringRequired("name", "John")
	assert.NoError(t, err)

	err = ValidateStringRequired("name", "")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrFieldRequired))
}

func TestValidator_ValidateRange(t *testing.T) {
	v := NewValidator()
	v.ValidateRange("year", 2020, 1900, 2100)
	assert.False(t, v.HasErrors())

	v = NewValidator()
	v.ValidateRange("year", 1800, 1900, 2100)
	assert.True(t, v.HasErrors())
}

func TestValidator_ValidatePositive(t *testing.T) {
	v := NewValidator()
	v.ValidatePositive("count", 10)
	assert.False(t, v.HasErrors())

	v = NewValidator()
	v.ValidatePositive("count", -1)
	assert.True(t, v.HasErrors())
}
