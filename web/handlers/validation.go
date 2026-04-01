package handlers

import (
	"errors"
	"strings"

	"github.com/go-playground/validator/v10"
)

// formatValidationErrors transforms go-playground/validator errors into
// user-friendly messages that don't expose internal Go struct names.
func formatValidationErrors(err error) string {
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		msgs := make([]string, 0, len(ve))
		for _, fe := range ve {
			// Use the field name in lowercase instead of the Go struct field name
			field := strings.ToLower(fe.Field())
			switch fe.Tag() {
			case "required":
				msgs = append(msgs, field+" is required")
			case "min":
				msgs = append(msgs, field+" must be at least "+fe.Param())
			case "max":
				msgs = append(msgs, field+" must be at most "+fe.Param())
			case "len":
				msgs = append(msgs, field+" must be exactly "+fe.Param()+" characters")
			case "latitude":
				msgs = append(msgs, field+" must be a valid latitude")
			case "longitude":
				msgs = append(msgs, field+" must be a valid longitude")
			default:
				msgs = append(msgs, field+" is invalid")
			}
		}
		return strings.Join(msgs, "; ")
	}
	// Non-validation error - return generic message to avoid leaking internals
	return "invalid request parameters"
}
