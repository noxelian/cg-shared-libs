// Package validation provides input validation utilities for gRPC and HTTP services.
package validation

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Field length limits
const (
	// MaxNameLength is the maximum length for name fields
	MaxNameLength = 255

	// MaxDescriptionLength is the maximum length for description/note fields
	MaxDescriptionLength = 2000

	// MaxPhoneLength is the maximum length for phone numbers
	MaxPhoneLength = 15

	// MaxEmailLength is the maximum length for email addresses
	MaxEmailLength = 255

	// MaxCodeLength is the maximum length for short codes (invite codes, verification codes)
	MaxCodeLength = 10

	// MaxMessageLength is the maximum length for messages
	MaxMessageLength = 10000

	// MaxUUIDLength is the maximum length for UUIDs
	MaxUUIDLength = 36

	// MaxAddressLength is the maximum length for address fields
	MaxAddressLength = 500

	// MaxURLLength is the maximum length for URLs
	MaxURLLength = 2000

	// MaxRefreshTokenLength is the maximum length for refresh tokens
	MaxRefreshTokenLength = 512

	// MaxDeviceIDLength is the maximum length for device IDs
	MaxDeviceIDLength = 36
)

// Validation errors
var (
	ErrFieldTooLong    = fmt.Errorf("field exceeds maximum length")
	ErrFieldRequired   = fmt.Errorf("field is required")
	ErrInvalidFormat   = fmt.Errorf("field has invalid format")
	ErrInvalidEmail    = fmt.Errorf("invalid email format")
	ErrInvalidPhone    = fmt.Errorf("invalid phone format")
	ErrInvalidUUID     = fmt.Errorf("invalid UUID format")
	ErrArrayTooLong    = fmt.Errorf("array exceeds maximum length")
	ErrNegativeValue   = fmt.Errorf("value cannot be negative")
	ErrValueOutOfRange = fmt.Errorf("value is out of range")
)

// ValidationError holds validation error details
type ValidationError struct {
	Field   string
	Message string
	Err     error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

// Validator provides validation methods
type Validator struct {
	errors []*ValidationError
}

// NewValidator creates a new validator
func NewValidator() *Validator {
	return &Validator{
		errors: make([]*ValidationError, 0),
	}
}

// HasErrors returns true if there are validation errors
func (v *Validator) HasErrors() bool {
	return len(v.errors) > 0
}

// Errors returns all validation errors
func (v *Validator) Errors() []*ValidationError {
	return v.errors
}

// Error returns the first error or nil
func (v *Validator) Error() error {
	if len(v.errors) == 0 {
		return nil
	}
	return v.errors[0]
}

// ErrorString returns a formatted error string
func (v *Validator) ErrorString() string {
	if len(v.errors) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, err := range v.errors {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(err.Error())
	}
	return sb.String()
}

// addError adds a validation error
func (v *Validator) addError(field, message string, err error) {
	v.errors = append(v.errors, &ValidationError{
		Field:   field,
		Message: message,
		Err:     err,
	})
}

// ValidateMaxLength validates string length
func (v *Validator) ValidateMaxLength(field, value string, maxLen int) *Validator {
	if utf8.RuneCountInString(value) > maxLen {
		v.addError(field, fmt.Sprintf("exceeds maximum length of %d characters", maxLen), ErrFieldTooLong)
	}
	return v
}

// ValidateRequired validates that a string is not empty
func (v *Validator) ValidateRequired(field, value string) *Validator {
	if strings.TrimSpace(value) == "" {
		v.addError(field, "is required", ErrFieldRequired)
	}
	return v
}

// ValidateRequiredMaxLength validates that a string is not empty and within max length
func (v *Validator) ValidateRequiredMaxLength(field, value string, maxLen int) *Validator {
	v.ValidateRequired(field, value)
	if value != "" {
		v.ValidateMaxLength(field, value, maxLen)
	}
	return v
}

// ValidateOptionalMaxLength validates string length only if value is not empty
func (v *Validator) ValidateOptionalMaxLength(field, value string, maxLen int) *Validator {
	if value != "" {
		v.ValidateMaxLength(field, value, maxLen)
	}
	return v
}

// ValidateName validates a name field
func (v *Validator) ValidateName(field, value string, required bool) *Validator {
	if required {
		v.ValidateRequiredMaxLength(field, value, MaxNameLength)
	} else {
		v.ValidateOptionalMaxLength(field, value, MaxNameLength)
	}
	return v
}

// ValidateDescription validates a description/note field
func (v *Validator) ValidateDescription(field, value string) *Validator {
	return v.ValidateOptionalMaxLength(field, value, MaxDescriptionLength)
}

// ValidatePhone validates a phone number field
func (v *Validator) ValidatePhone(field, value string, required bool) *Validator {
	if required {
		v.ValidateRequired(field, value)
	}
	if value != "" {
		v.ValidateMaxLength(field, value, MaxPhoneLength)
		// Basic phone format check: starts with + and contains only digits
		if !isValidPhoneFormat(value) {
			v.addError(field, "must be a valid phone number", ErrInvalidPhone)
		}
	}
	return v
}

// ValidateEmail validates an email field
func (v *Validator) ValidateEmail(field, value string, required bool) *Validator {
	if required {
		v.ValidateRequired(field, value)
	}
	if value != "" {
		v.ValidateMaxLength(field, value, MaxEmailLength)
		if !isValidEmailFormat(value) {
			v.addError(field, "must be a valid email address", ErrInvalidEmail)
		}
	}
	return v
}

// ValidateCode validates a short code field (verification codes, invite codes)
func (v *Validator) ValidateCode(field, value string, required bool) *Validator {
	if required {
		v.ValidateRequiredMaxLength(field, value, MaxCodeLength)
	} else {
		v.ValidateOptionalMaxLength(field, value, MaxCodeLength)
	}
	return v
}

// ValidateMessage validates a message field
func (v *Validator) ValidateMessage(field, value string, required bool) *Validator {
	if required {
		v.ValidateRequiredMaxLength(field, value, MaxMessageLength)
	} else {
		v.ValidateOptionalMaxLength(field, value, MaxMessageLength)
	}
	return v
}

// ValidateUUID validates a UUID field
func (v *Validator) ValidateUUID(field, value string, required bool) *Validator {
	if required {
		v.ValidateRequired(field, value)
	}
	if value != "" {
		v.ValidateMaxLength(field, value, MaxUUIDLength)
		if !isValidUUIDFormat(value) {
			v.addError(field, "must be a valid UUID", ErrInvalidUUID)
		}
	}
	return v
}

// ValidateAddress validates an address field
func (v *Validator) ValidateAddress(field, value string) *Validator {
	return v.ValidateOptionalMaxLength(field, value, MaxAddressLength)
}

// ValidateURL validates a URL field
func (v *Validator) ValidateURL(field, value string) *Validator {
	return v.ValidateOptionalMaxLength(field, value, MaxURLLength)
}

// ValidateArrayLength validates array length
func (v *Validator) ValidateArrayLength(field string, length, maxLen int) *Validator {
	if length > maxLen {
		v.addError(field, fmt.Sprintf("exceeds maximum length of %d items", maxLen), ErrArrayTooLong)
	}
	return v
}

// ValidatePositive validates that a number is positive
func (v *Validator) ValidatePositive(field string, value int64) *Validator {
	if value < 0 {
		v.addError(field, "must be positive", ErrNegativeValue)
	}
	return v
}

// ValidateRange validates that a number is within range
func (v *Validator) ValidateRange(field string, value, minimum, maximum int64) *Validator {
	if value < minimum || value > maximum {
		v.addError(field, fmt.Sprintf("must be between %d and %d", minimum, maximum), ErrValueOutOfRange)
	}
	return v
}

// Helper functions

var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	phoneRegex = regexp.MustCompile(`^\+?\d{7,15}$`)
	uuidRegex  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func isValidEmailFormat(email string) bool {
	return emailRegex.MatchString(email)
}

func isValidPhoneFormat(phone string) bool {
	return phoneRegex.MatchString(phone)
}

func isValidUUIDFormat(uuid string) bool {
	return uuidRegex.MatchString(uuid)
}

// Convenience functions for simple validations

// ValidateStringMaxLength validates a single string field
func ValidateStringMaxLength(field, value string, maxLen int) error {
	if utf8.RuneCountInString(value) > maxLen {
		return &ValidationError{
			Field:   field,
			Message: fmt.Sprintf("exceeds maximum length of %d characters", maxLen),
			Err:     ErrFieldTooLong,
		}
	}
	return nil
}

// ValidateStringRequired validates that a string is not empty
func ValidateStringRequired(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return &ValidationError{
			Field:   field,
			Message: "is required",
			Err:     ErrFieldRequired,
		}
	}
	return nil
}

// GRPCError returns a gRPC InvalidArgument status error from the validator.
// Returns nil if there are no validation errors.
func (v *Validator) GRPCError() error {
	if !v.HasErrors() {
		return nil
	}
	return status.Error(codes.InvalidArgument, v.ErrorString())
}

// ValidatePositiveID validates that an ID is > 0
func (v *Validator) ValidatePositiveID(field string, value int64) *Validator {
	if value <= 0 {
		v.addError(field, "must be a positive integer", ErrNegativeValue)
	}
	return v
}

// ValidatePageSize validates pagination parameters
func (v *Validator) ValidatePageSize(page, pageSize, maxPageSize int32) *Validator {
	if page < 0 {
		v.addError("page", "must be >= 0", ErrValueOutOfRange)
	}
	if pageSize < 1 || pageSize > maxPageSize {
		v.addError("page_size", fmt.Sprintf("must be between 1 and %d", maxPageSize), ErrValueOutOfRange)
	}
	return v
}

// ValidateAmount validates that an amount (financial) is positive
func (v *Validator) ValidateAmount(field string, value float64) *Validator {
	if value <= 0 {
		v.addError(field, "must be greater than 0", ErrNegativeValue)
	}
	return v
}
