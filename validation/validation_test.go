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

func TestValidator_ValidateCode(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		required bool
		hasError bool
	}{
		{name: "valid code required", field: "code", value: "ABC123", required: true, hasError: false},
		{name: "empty code required", field: "code", value: "", required: true, hasError: true},
		{name: "empty code optional", field: "code", value: "", required: false, hasError: false},
		{name: "code too long", field: "code", value: "12345678901", required: false, hasError: true},
		{name: "code at limit", field: "code", value: "1234567890", required: true, hasError: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateCode(tt.field, tt.value, tt.required)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidateMessage(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		value    string
		required bool
		hasError bool
	}{
		{name: "valid message required", field: "msg", value: "Hello world", required: true, hasError: false},
		{name: "empty message required", field: "msg", value: "", required: true, hasError: true},
		{name: "empty message optional", field: "msg", value: "", required: false, hasError: false},
		{name: "message too long", field: "msg", value: strings.Repeat("x", 10001), required: false, hasError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateMessage(tt.field, tt.value, tt.required)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidateAddress(t *testing.T) {
	v := NewValidator()
	v.ValidateAddress("address", "123 Main St")
	assert.False(t, v.HasErrors())

	v = NewValidator()
	v.ValidateAddress("address", strings.Repeat("x", 501))
	assert.True(t, v.HasErrors())

	v = NewValidator()
	v.ValidateAddress("address", "")
	assert.False(t, v.HasErrors())
}

func TestValidator_ValidateURL(t *testing.T) {
	v := NewValidator()
	v.ValidateURL("website", "https://example.com")
	assert.False(t, v.HasErrors())

	v = NewValidator()
	v.ValidateURL("website", strings.Repeat("x", 2001))
	assert.True(t, v.HasErrors())

	v = NewValidator()
	v.ValidateURL("website", "")
	assert.False(t, v.HasErrors())
}

func TestValidator_GRPCError(t *testing.T) {
	v := NewValidator()
	assert.Nil(t, v.GRPCError())

	v = NewValidator()
	v.ValidateRequired("name", "")
	err := v.GRPCError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestValidator_ValidatePositiveID(t *testing.T) {
	tests := []struct {
		name     string
		value    int64
		hasError bool
	}{
		{name: "positive value", value: 1, hasError: false},
		{name: "large positive", value: 999999, hasError: false},
		{name: "zero", value: 0, hasError: true},
		{name: "negative", value: -1, hasError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidatePositiveID("id", tt.value)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidatePageSize(t *testing.T) {
	tests := []struct {
		name     string
		page     int32
		pageSize int32
		maxSize  int32
		hasError bool
	}{
		{name: "valid page and size", page: 0, pageSize: 10, maxSize: 100, hasError: false},
		{name: "negative page", page: -1, pageSize: 10, maxSize: 100, hasError: true},
		{name: "page size too small", page: 0, pageSize: 0, maxSize: 100, hasError: true},
		{name: "page size too large", page: 0, pageSize: 101, maxSize: 100, hasError: true},
		{name: "page size at max", page: 0, pageSize: 100, maxSize: 100, hasError: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidatePageSize(tt.page, tt.pageSize, tt.maxSize)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_ValidateAmount(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		hasError bool
	}{
		{name: "positive amount", value: 100.50, hasError: false},
		{name: "zero amount", value: 0, hasError: true},
		{name: "negative amount", value: -10, hasError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator()
			v.ValidateAmount("price", tt.value)
			assert.Equal(t, tt.hasError, v.HasErrors())
		})
	}
}

func TestValidator_Error_NoErrors(t *testing.T) {
	v := NewValidator()
	assert.Nil(t, v.Error())
}

func TestValidator_Error_ReturnsFirst(t *testing.T) {
	v := NewValidator()
	v.ValidateRequired("name", "")
	v.ValidateRequired("email", "")

	err := v.Error()
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestValidator_ValidateName_OptionalEmpty(t *testing.T) {
	v := NewValidator()
	v.ValidateName("name", "", false)
	assert.False(t, v.HasErrors())
}

func TestValidator_ValidateName_OptionalTooLong(t *testing.T) {
	v := NewValidator()
	v.ValidateName("name", strings.Repeat("a", 256), false)
	assert.True(t, v.HasErrors())
}

func TestValidator_ErrorString_NoErrors(t *testing.T) {
	v := NewValidator()
	assert.Empty(t, v.ErrorString())
}

func TestValidator_ErrorString_MultipleErrors(t *testing.T) {
	v := NewValidator()
	v.ValidateRequired("name", "")
	v.ValidateRequired("email", "")

	errStr := v.ErrorString()
	assert.Contains(t, errStr, "name")
	assert.Contains(t, errStr, "email")
	assert.Contains(t, errStr, "; ")
}
