// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// BindSource identifies where to read a parameter from.
type BindSource string

const (
	// BindSourceQuery reads from URL query parameters.
	BindSourceQuery BindSource = "query"
	// BindSourcePath reads from URL path segments.
	BindSourcePath BindSource = "path"
	// BindSourceHeader reads from HTTP headers.
	BindSourceHeader BindSource = "header"
)

// ValidationError reports a single field validation failure.
type ValidationError struct {
	Field string
	Rule  string
	Text  string
}

// Error returns a human-readable validation message.
func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Text != "" {
		return e.Text
	}
	return fmt.Sprintf("field %s failed %s validation", e.Field, e.Rule)
}

// BindJSON decodes the request body as JSON and validates the result.
func BindJSON(r *http.Request, v any) error {
	if err := decodeJSON(r, v); err != nil {
		return err
	}
	return Validate(v)
}

func BindQuery(r *http.Request, v any) error {
	if err := bindValues(v, BindSourceQuery, func(key string) []string { return r.URL.Query()[key] }); err != nil {
		return err
	}
	return Validate(v)
}

func BindPath(r *http.Request, v any) error {
	if err := bindValues(v, BindSourcePath, func(key string) []string {
		if value := r.PathValue(key); value != "" {
			return []string{value}
		}
		return nil
	}); err != nil {
		return err
	}
	return Validate(v)
}

func BindHeader(r *http.Request, v any) error {
	if err := bindValues(v, BindSourceHeader, func(key string) []string { return r.Header.Values(key) }); err != nil {
		return err
	}
	return Validate(v)
}

func BindRequest(r *http.Request, v any) error {
	if r.Body != nil && r.Body != http.NoBody && r.Method != http.MethodGet && r.Method != http.MethodDelete {
		if err := decodeJSON(r, v); err != nil {
			return err
		}
	}
	if err := bindValues(v, BindSourcePath, func(key string) []string {
		if value := r.PathValue(key); value != "" {
			return []string{value}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := bindValues(v, BindSourceQuery, func(key string) []string { return r.URL.Query()[key] }); err != nil {
		return err
	}
	if err := bindValues(v, BindSourceHeader, func(key string) []string { return r.Header.Values(key) }); err != nil {
		return err
	}
	return Validate(v)
}

func Validate(v any) error {
	value, err := structValue(v)
	if err != nil {
		return err
	}
	return validateStruct(value)
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("decode json body: %w", err)
	}
	return nil
}

func bindValues(v any, source BindSource, lookup func(key string) []string) error {
	value, err := structValue(v)
	if err != nil {
		return err
	}
	return bindStruct(value, source, lookup)
}

func structValue(v any) (reflect.Value, error) {
	if v == nil {
		return reflect.Value{}, fmt.Errorf("bind target is nil")
	}
	value := reflect.ValueOf(v)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return reflect.Value{}, fmt.Errorf("bind target must be a non-nil pointer")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("bind target must point to a struct")
	}
	return value, nil
}

func bindStruct(value reflect.Value, source BindSource, lookup func(key string) []string) error {
	typeOf := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		structField := typeOf.Field(i)
		if structField.PkgPath != "" {
			continue
		}
		if field.Kind() == reflect.Struct && structField.Anonymous {
			if err := bindStruct(field, source, lookup); err != nil {
				return err
			}
			continue
		}
		name, ok := bindingName(structField, source)
		if !ok {
			continue
		}
		values := lookup(name)
		if len(values) == 0 {
			continue
		}
		if err := setFieldValue(field, values); err != nil {
			return fmt.Errorf("bind %s field %s: %w", source, structField.Name, err)
		}
	}
	return nil
}

func bindingName(field reflect.StructField, source BindSource) (string, bool) {
	tag := field.Tag.Get(string(source))
	if tag == "-" {
		return "", false
	}
	if tag == "" && source == BindSourceQuery {
		tag = field.Tag.Get("form")
	}
	if tag == "" {
		return "", false
	}
	name := strings.Split(tag, ",")[0]
	return name, name != ""
}

func setFieldValue(field reflect.Value, values []string) error {
	if !field.CanSet() {
		return nil
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		return setFieldValue(field.Elem(), values)
	}
	if field.Kind() == reflect.Slice {
		slice := reflect.MakeSlice(field.Type(), 0, len(values))
		for _, value := range values {
			elem := reflect.New(field.Type().Elem()).Elem()
			if err := setScalar(elem, value); err != nil {
				return err
			}
			slice = reflect.Append(slice, elem)
		}
		field.Set(slice)
		return nil
	}
	return setScalar(field, values[len(values)-1])
}

func setScalar(field reflect.Value, value string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(parsed)
	default:
		return fmt.Errorf("unsupported kind %s", field.Kind())
	}
	return nil
}

func validateStruct(value reflect.Value) error {
	typeOf := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		structField := typeOf.Field(i)
		if structField.PkgPath != "" {
			continue
		}
		if field.Kind() == reflect.Struct && structField.Anonymous {
			if err := validateStruct(field); err != nil {
				return err
			}
			continue
		}
		rules := strings.Split(structField.Tag.Get("validate"), ",")
		for _, rule := range rules {
			rule = strings.TrimSpace(rule)
			if rule == "" || rule == "-" {
				continue
			}
			if err := validateField(structField.Name, field, rule); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateField(name string, field reflect.Value, rule string) error {
	switch {
	case rule == "required":
		if isZero(field) {
			return &ValidationError{Field: name, Rule: rule}
		}
	case strings.HasPrefix(rule, "min="):
		limit, err := strconv.ParseFloat(strings.TrimPrefix(rule, "min="), 64)
		if err != nil {
			return fmt.Errorf("invalid min rule for %s: %w", name, err)
		}
		if numericValue(field) < limit {
			return &ValidationError{Field: name, Rule: rule}
		}
	case strings.HasPrefix(rule, "max="):
		limit, err := strconv.ParseFloat(strings.TrimPrefix(rule, "max="), 64)
		if err != nil {
			return fmt.Errorf("invalid max rule for %s: %w", name, err)
		}
		if numericValue(field) > limit {
			return &ValidationError{Field: name, Rule: rule}
		}
	case strings.HasPrefix(rule, "oneof="):
		allowed := strings.Fields(strings.TrimPrefix(rule, "oneof="))
		got := fmt.Sprint(fieldValue(field))
		for _, item := range allowed {
			if got == item {
				return nil
			}
		}
		return &ValidationError{Field: name, Rule: rule}
	}
	return nil
}

func isZero(field reflect.Value) bool {
	if field.Kind() == reflect.Pointer {
		return field.IsNil() || isZero(field.Elem())
	}
	return field.IsZero()
}

func fieldValue(field reflect.Value) any {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return nil
		}
		return fieldValue(field.Elem())
	}
	return field.Interface()
}

func numericValue(field reflect.Value) float64 {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return 0
		}
		return numericValue(field.Elem())
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(field.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(field.Uint())
	case reflect.Float32, reflect.Float64:
		return field.Float()
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return float64(field.Len())
	default:
		return 0
	}
}
