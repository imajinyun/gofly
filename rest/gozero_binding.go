package rest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// BindGoZeroRequest binds a request using the go-zero field-tag contract.
// In addition to normal path, form/query, header, and JSON binding, it applies
// optional, default, options, and range modifiers from those source tags.
func BindGoZeroRequest(r *http.Request, v any) error {
	return bindGoZeroRequest(r, v, nil)
}

func bindGoZeroRequest(r *http.Request, v any, validator Validator) error {
	value, err := structValue(v)
	if err != nil {
		return invalidRequestError(err)
	}

	if err := bindValues(v, BindSourcePath, func(key string) []string {
		if value := r.PathValue(key); value != "" {
			return []string{value}
		}
		return nil
	}); err != nil {
		return invalidRequestError(err)
	}
	if err := applyGoZeroSourceTags(value, BindSourcePath, func(key string) []string {
		if value := r.PathValue(key); value != "" {
			return []string{value}
		}
		return nil
	}); err != nil {
		return invalidRequestError(err)
	}

	query := r.URL.Query()
	if err := r.ParseForm(); err != nil {
		return invalidRequestError(fmt.Errorf("parse form: %w", err))
	}
	form := r.Form
	if len(form) == 0 {
		form = query
	}
	if err := bindValues(v, BindSourceQuery, func(key string) []string { return nonEmptyGoZeroValues(form[key]) }); err != nil {
		return invalidRequestError(err)
	}
	if err := applyGoZeroSourceTags(value, BindSourceQuery, func(key string) []string { return nonEmptyGoZeroValues(form[key]) }); err != nil {
		return invalidRequestError(err)
	}

	if err := bindValues(v, BindSourceHeader, func(key string) []string { return r.Header.Values(key) }); err != nil {
		return invalidRequestError(err)
	}
	if err := applyGoZeroSourceTags(value, BindSourceHeader, func(key string) []string { return r.Header.Values(key) }); err != nil {
		return invalidRequestError(err)
	}

	jsonValues := map[string]json.RawMessage{}
	if r.Body != nil && r.Body != http.NoBody && r.ContentLength > 0 && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return invalidRequestError(fmt.Errorf("read json body: %w", err))
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(v); err != nil {
			return invalidRequestError(fmt.Errorf("decode json body: %w", err))
		}
		if err := json.Unmarshal(body, &jsonValues); err != nil {
			return invalidRequestError(fmt.Errorf("inspect json body: %w", err))
		}
	}
	if err := applyGoZeroJSONTags(value, jsonValues); err != nil {
		return invalidRequestError(err)
	}
	return invalidRequestError(validateWith(v, validator))
}

func nonEmptyGoZeroValues(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

type goZeroFieldOptions struct {
	optional     bool
	defaultSet   bool
	defaultValue string
	options      []string
	rangeRule    *goZeroNumberRange
}

type goZeroNumberRange struct {
	left         *float64
	right        *float64
	leftInclude  bool
	rightInclude bool
}

func applyGoZeroSourceTags(value reflect.Value, source BindSource, lookup func(string) []string) error {
	typeOf := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		structField := typeOf.Field(index)
		if structField.PkgPath != "" {
			continue
		}
		if structField.Anonymous && indirectType(structField.Type).Kind() == reflect.Struct {
			if field.Kind() == reflect.Pointer {
				if field.IsNil() {
					continue
				}
				field = field.Elem()
			}
			if err := applyGoZeroSourceTags(field, source, lookup); err != nil {
				return err
			}
			continue
		}
		raw, ok := goZeroSourceTag(structField, source)
		if !ok {
			continue
		}
		name, options, err := parseGoZeroFieldTag(raw)
		if err != nil {
			return fmt.Errorf("field %s has invalid %s tag: %w", structField.Name, source, err)
		}
		if name == "-" {
			continue
		}
		if name == "" {
			name = structField.Name
		}
		present := len(lookup(name)) > 0
		if err := applyGoZeroFieldOptions(structField.Name, field, present, options); err != nil {
			return err
		}
	}
	return nil
}

func applyGoZeroJSONTags(value reflect.Value, values map[string]json.RawMessage) error {
	typeOf := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		structField := typeOf.Field(index)
		if structField.PkgPath != "" {
			continue
		}
		if structField.Anonymous && indirectType(structField.Type).Kind() == reflect.Struct {
			if field.Kind() == reflect.Pointer {
				if field.IsNil() {
					continue
				}
				field = field.Elem()
			}
			if err := applyGoZeroJSONTags(field, values); err != nil {
				return err
			}
			continue
		}
		raw, ok := structField.Tag.Lookup("json")
		if !ok {
			continue
		}
		name, options, err := parseGoZeroFieldTag(raw)
		if err != nil {
			return fmt.Errorf("field %s has invalid json tag: %w", structField.Name, err)
		}
		if name == "-" {
			continue
		}
		if name == "" {
			name = structField.Name
		}
		_, present := lookupJSONField(values, name)
		if err := applyGoZeroFieldOptions(structField.Name, field, present, options); err != nil {
			return err
		}
	}
	return nil
}

func goZeroSourceTag(field reflect.StructField, source BindSource) (string, bool) {
	if source == BindSourceQuery {
		if value, ok := field.Tag.Lookup("query"); ok {
			return value, true
		}
		return field.Tag.Lookup("form")
	}
	return field.Tag.Lookup(string(source))
}

func lookupJSONField(values map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	if value, ok := values[name]; ok {
		return value, true
	}
	for key, value := range values {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return nil, false
}

func parseGoZeroFieldTag(raw string) (string, goZeroFieldOptions, error) {
	parts, err := splitGoZeroTagParts(raw)
	if err != nil {
		return "", goZeroFieldOptions{}, err
	}
	if len(parts) == 0 {
		return "", goZeroFieldOptions{}, nil
	}
	opts := goZeroFieldOptions{}
	for _, option := range parts[1:] {
		switch {
		case option == "optional":
			opts.optional = true
		case strings.HasPrefix(option, "optional="):
			return "", goZeroFieldOptions{}, fmt.Errorf("conditional optional is not supported")
		case strings.HasPrefix(option, "default="):
			opts.defaultSet = true
			opts.defaultValue = strings.TrimPrefix(option, "default=")
			if opts.defaultValue == "" {
				return "", goZeroFieldOptions{}, fmt.Errorf("default value is empty")
			}
		case strings.HasPrefix(option, "options="):
			values := strings.TrimSpace(strings.TrimPrefix(option, "options="))
			if values == "" {
				return "", goZeroFieldOptions{}, fmt.Errorf("options value is empty")
			}
			if strings.HasPrefix(values, "[") && strings.HasSuffix(values, "]") {
				values = strings.TrimSpace(values[1 : len(values)-1])
				opts.options = strings.FieldsFunc(values, func(r rune) bool { return r == ',' })
			} else {
				opts.options = strings.Split(values, "|")
			}
			if len(opts.options) == 0 {
				return "", goZeroFieldOptions{}, fmt.Errorf("options value is empty")
			}
		case strings.HasPrefix(option, "range="):
			rule := strings.TrimPrefix(option, "range=")
			parsed, err := parseGoZeroNumberRange(rule)
			if err != nil {
				return "", goZeroFieldOptions{}, err
			}
			opts.rangeRule = parsed
		}
	}
	return parts[0], opts, nil
}

func splitGoZeroTagParts(raw string) ([]string, error) {
	var parts []string
	var part strings.Builder
	depth := 0
	for _, char := range raw {
		switch char {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced modifier delimiters")
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(part.String()))
				part.Reset()
				continue
			}
		}
		part.WriteRune(char)
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced modifier delimiters")
	}
	parts = append(parts, strings.TrimSpace(part.String()))
	return parts, nil
}

func parseGoZeroNumberRange(raw string) (*goZeroNumberRange, error) {
	if len(raw) < 3 || (raw[0] != '[' && raw[0] != '(') || (raw[len(raw)-1] != ']' && raw[len(raw)-1] != ')') {
		return nil, fmt.Errorf("invalid range %q", raw)
	}
	leftText, rightText, ok := strings.Cut(raw[1:len(raw)-1], ":")
	if !ok || (strings.TrimSpace(leftText) == "" && strings.TrimSpace(rightText) == "") {
		return nil, fmt.Errorf("invalid range %q", raw)
	}
	result := &goZeroNumberRange{leftInclude: raw[0] == '[', rightInclude: raw[len(raw)-1] == ']'}
	if strings.TrimSpace(leftText) != "" {
		value, err := strconv.ParseFloat(strings.TrimSpace(leftText), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid range %q", raw)
		}
		result.left = &value
	}
	if strings.TrimSpace(rightText) != "" {
		value, err := strconv.ParseFloat(strings.TrimSpace(rightText), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid range %q", raw)
		}
		result.right = &value
	}
	if result.left != nil && result.right != nil {
		if *result.left > *result.right || *result.left == *result.right && (!result.leftInclude || !result.rightInclude) {
			return nil, fmt.Errorf("invalid range %q", raw)
		}
	}
	return result, nil
}

func applyGoZeroFieldOptions(name string, field reflect.Value, present bool, options goZeroFieldOptions) error {
	if !present {
		if options.defaultSet {
			if err := setFieldValue(field, []string{options.defaultValue}); err != nil {
				return fmt.Errorf("apply default for field %s: %w", name, err)
			}
		} else if options.optional {
			return nil
		} else {
			return &ValidationError{Field: name, Rule: "required"}
		}
	}
	if len(options.options) > 0 {
		actual := fmt.Sprint(fieldValue(field))
		matched := false
		for _, allowed := range options.options {
			if actual == strings.TrimSpace(allowed) {
				matched = true
				break
			}
		}
		if !matched {
			return &ValidationError{Field: name, Rule: "options=" + strings.Join(options.options, "|")}
		}
	}
	if options.rangeRule != nil {
		actual, ok := goZeroNumericValue(field)
		if !ok {
			return fmt.Errorf("field %s uses range on unsupported type %s", name, field.Type())
		}
		rule := options.rangeRule
		if rule.left != nil && (actual < *rule.left || (!rule.leftInclude && actual == *rule.left)) {
			return &ValidationError{Field: name, Rule: "range"}
		}
		if rule.right != nil && (actual > *rule.right || (!rule.rightInclude && actual == *rule.right)) {
			return &ValidationError{Field: name, Rule: "range"}
		}
	}
	return nil
}

func goZeroNumericValue(value reflect.Value) (float64, bool) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, false
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	default:
		return 0, false
	}
}
