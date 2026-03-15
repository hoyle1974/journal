package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-playground/validator/v10"
)

// DecodeAndValidate decodes the request body as JSON into v (must be a pointer to a struct),
// then runs validation if validate is non-nil. Returns an error suitable for 400 responses.
func DecodeAndValidate(r *http.Request, v interface{}, validate *validator.Validate) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if validate == nil {
		return nil
	}
	if err := validate.Struct(v); err != nil {
		var verr validator.ValidationErrors
		if errors.As(err, &verr) {
			return fmt.Errorf("validation failed: %s", verr.Error())
		}
		return fmt.Errorf("validation failed: %w", err)
	}
	return nil
}
