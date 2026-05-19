package handlers

import (
	"errors"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// formatValidationErrors transforms go-playground/validator errors into
// user-friendly messages that don't expose internal Go struct names and
// pick the right unit for each field kind ("characters" for strings,
// "items" for arrays/slices/maps, bare numeric bound for ints/floats).
func formatValidationErrors(err error) string {
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		msgs := make([]string, 0, len(ve))
		for _, fe := range ve {
			// Field() returns the JSON-tag name when the validator has been
			// configured with RegisterTagNameFunc (see web/handlers/api.go).
			// That's true everywhere we run validation; if a caller forgot to
			// wire the tag-name func, fall through to the lowercased Go name
			// rather than crash.
			field := fe.Field()
			if field == "" {
				field = strings.ToLower(fe.StructField())
			}
			switch fe.Tag() {
			case "required":
				msgs = append(msgs, field+" is required")
			case "min":
				msgs = append(msgs, formatMinMax(field, "at least", fe))
			case "max":
				msgs = append(msgs, formatMinMax(field, "at most", fe))
			case "len":
				msgs = append(msgs, field+" must be exactly "+fe.Param()+" characters")
			case "latitude":
				msgs = append(msgs, field+" must be a valid latitude")
			case "longitude":
				msgs = append(msgs, field+" must be a valid longitude")
			case "oneof":
				msgs = append(msgs, field+" must be one of: "+fe.Param())
			default:
				msgs = append(msgs, field+" is invalid")
			}
		}
		return strings.Join(msgs, "; ")
	}
	// Non-validation error - return generic message to avoid leaking internals
	return "invalid request parameters"
}

// formatMinMax picks the right unit for min/max bound messages so e.g.
// max_results=501 reads "max_results must be at most 500" instead of
// the previous "maxresults must be at most 500 characters".
func formatMinMax(field, direction string, fe validator.FieldError) string {
	switch fe.Kind() {
	case reflect.String:
		return field + " must be " + direction + " " + fe.Param() + " characters"
	case reflect.Slice, reflect.Array, reflect.Map:
		return field + " must have " + direction + " " + fe.Param() + " items"
	default:
		// int, float, etc. — the bound is a numeric value, not a length.
		return field + " must be " + direction + " " + fe.Param()
	}
}
