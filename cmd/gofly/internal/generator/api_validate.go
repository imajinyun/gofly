package generator

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var apiPathParamNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var apiRouteProfileNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

var apiBuiltinTypes = map[string]struct{}{
	"any":         {},
	"bool":        {},
	"byte":        {},
	"bytes":       {},
	"error":       {},
	"float":       {},
	"float32":     {},
	"float64":     {},
	"int":         {},
	"int8":        {},
	"int16":       {},
	"int32":       {},
	"int64":       {},
	"interface{}": {},
	"rune":        {},
	"string":      {},
	"time.Time":   {},
	"uint":        {},
	"uint8":       {},
	"uint16":      {},
	"uint32":      {},
	"uint64":      {},
}

func ValidateAPI(doc IDLDocument) error {
	issues := validateAPIIssues(doc)
	if len(issues) == 0 {
		return nil
	}
	return errors.New("validate api: " + strings.Join(issues, "; "))
}

func validateAPIIssues(doc IDLDocument) []string {
	issues := []string{}
	types := map[string]IDLMessage{}
	for _, msg := range doc.Messages {
		name := exportName(msg.Name)
		if _, ok := types[name]; ok {
			issues = append(issues, fmt.Sprintf("duplicate type %s", name))
			continue
		}
		types[name] = msg
	}

	for _, msg := range doc.Messages {
		issues = append(issues, validateAPIMessage(msg, types)...)
	}

	routes := map[string]struct{}{}
	handlers := map[string]struct{}{}
	for _, svc := range doc.Services {
		if strings.TrimSpace(svc.Name) == "" {
			issues = append(issues, "service name is required")
		}
		issues = append(issues, validateAPIServerRouteOptions(svc)...)
		for _, method := range svc.Methods {
			issues = append(issues, validateAPIMethod(method, types, routes, handlers)...)
		}
	}
	return issues
}

func validateAPIServerRouteOptions(svc IDLService) []string {
	values := svc.Server.Values
	if len(values) == 0 {
		return nil
	}
	var issues []string
	serviceName := strings.TrimSpace(svc.Name)
	if raw, ok := values["timeout"]; ok {
		duration, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil || duration <= 0 {
			issues = append(issues, fmt.Sprintf("service %s has invalid route timeout %q", serviceName, raw))
		}
	}
	maxBytes, hasMaxBytes := values["maxbytes"]
	maxBodyBytes, hasMaxBodyBytes := values["maxbodybytes"]
	if hasMaxBytes && hasMaxBodyBytes && strings.TrimSpace(maxBytes) != strings.TrimSpace(maxBodyBytes) {
		issues = append(issues, fmt.Sprintf("service %s declares conflicting maxBytes and maxBodyBytes values", serviceName))
	} else if hasMaxBytes || hasMaxBodyBytes {
		raw := maxBytes
		if !hasMaxBytes {
			raw = maxBodyBytes
		}
		value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || value <= 0 {
			issues = append(issues, fmt.Sprintf("service %s has invalid route max body bytes %q", serviceName, raw))
		}
	}
	if raw, ok := values["sse"]; ok {
		if _, err := strconv.ParseBool(strings.TrimSpace(raw)); err != nil {
			issues = append(issues, fmt.Sprintf("service %s has invalid route sse value %q", serviceName, raw))
		}
	}
	if raw, ok := values["signature"]; ok {
		name := strings.TrimSpace(raw)
		if !apiRouteProfileNamePattern.MatchString(name) {
			issues = append(issues, fmt.Sprintf("service %s has invalid signature profile %q", serviceName, raw))
		}
	}
	if signature := strings.TrimSpace(values["signature"]); signature != "" && signature == strings.TrimSpace(svc.Server.JWT) {
		issues = append(issues, fmt.Sprintf("service %s reuses %q for JWT and signature configuration", serviceName, signature))
	}
	if raw, ok := values["priority"]; ok {
		issues = append(issues, fmt.Sprintf("service %s declares unsupported route priority %q: gofly has no request-priority shedding policy", serviceName, raw))
	}
	return issues
}

func validateAPIMessage(msg IDLMessage, types map[string]IDLMessage) []string {
	issues := []string{}
	messageName := exportName(msg.Name)
	fields := map[string]struct{}{}
	for _, field := range msg.Fields {
		fieldName := exportName(field.Name)
		if _, ok := fields[fieldName]; ok {
			issues = append(issues, fmt.Sprintf("duplicate field %s.%s", messageName, fieldName))
			continue
		}
		fields[fieldName] = struct{}{}
		issues = append(issues, validateAPIFieldTag(messageName, field)...)
		fieldType := apiBaseType(field.Type)
		if field.Inline {
			if isAPIBuiltinType(fieldType) || strings.HasPrefix(strings.TrimSpace(field.Type), "[]") {
				issues = append(issues, fmt.Sprintf("inline field %s must reference a struct", messageName))
				continue
			}
			if _, ok := types[exportName(fieldType)]; !ok {
				issues = append(issues, fmt.Sprintf("unknown inline field type %s %s", messageName, fieldType))
			}
			continue
		}
		if !isAPIBuiltinType(fieldType) {
			if _, ok := types[exportName(fieldType)]; !ok {
				issues = append(issues, fmt.Sprintf("unknown field type %s.%s %s", messageName, fieldName, field.Type))
			}
		}
	}
	return issues
}

func validateAPIFieldTag(messageName string, field IDLField) []string {
	if strings.TrimSpace(field.Tag) == "" {
		return nil
	}
	if err := validateAPIStructTag(field.Tag); err != nil {
		return []string{fmt.Sprintf("field %s.%s has invalid struct tag %q", messageName, exportName(field.Name), field.Tag)}
	}
	for _, key := range []string{"json", "form", "path", "header"} {
		raw, ok := lookupAPIStructTag(field.Tag, key)
		if !ok {
			continue
		}
		if err := validateGoZeroAPIFieldTag(raw); err != nil {
			return []string{fmt.Sprintf("field %s.%s has invalid %s tag: %v", messageName, exportName(field.Name), key, err)}
		}
	}
	return nil
}

func validateAPIStructTag(tag string) error {
	for field := 0; tag != ""; field++ {
		if field > 0 && tag[0] != ' ' {
			return errors.New("struct tag fields must be separated by spaces")
		}
		tag = strings.TrimLeft(tag, " ")
		if tag == "" {
			return nil
		}
		colon := strings.IndexByte(tag, ':')
		if colon <= 0 || colon+1 >= len(tag) || tag[colon+1] != '"' {
			return errors.New("invalid key/value syntax")
		}
		for _, char := range tag[:colon] {
			if char <= ' ' || char == '"' || char == 0x7f {
				return errors.New("invalid key syntax")
			}
		}
		quoted, remaining, err := consumeAPIQuotedTagValue(tag[colon+1:])
		if err != nil {
			return err
		}
		if _, err := strconv.Unquote(quoted); err != nil {
			return errors.New("invalid quoted value")
		}
		tag = remaining
	}
	return nil
}

func lookupAPIStructTag(tag, target string) (string, bool) {
	for tag != "" {
		tag = strings.TrimLeft(tag, " 	")
		colon := strings.IndexByte(tag, ':')
		if colon <= 0 || colon+1 >= len(tag) {
			return "", false
		}
		key := tag[:colon]
		quoted, remaining, err := consumeAPIQuotedTagValue(tag[colon+1:])
		if err != nil {
			return "", false
		}
		if key == target {
			value, err := strconv.Unquote(quoted)
			return value, err == nil
		}
		tag = remaining
	}
	return "", false
}

func consumeAPIQuotedTagValue(tag string) (string, string, error) {
	if len(tag) < 2 || tag[0] != '"' {
		return "", "", errors.New("invalid quoted value")
	}
	for index := 1; index < len(tag); {
		if tag[index] == '"' {
			return tag[:index+1], tag[index+1:], nil
		}
		if tag[index] == '\\' {
			index++
			if index >= len(tag) {
				break
			}
		}
		_, size := utf8.DecodeRuneInString(tag[index:])
		index += size
	}
	return "", "", errors.New("invalid quoted value")
}

func validateGoZeroAPIFieldTag(raw string) error {
	parts, err := splitGoZeroAPITagParts(raw)
	if err != nil {
		return err
	}
	for _, rawOption := range parts[1:] {
		option := strings.TrimSpace(rawOption)
		switch {
		case option == "omitempty" || option == "optional" || option == "string" || option == "inherit":
		case strings.HasPrefix(option, "optional="):
			return errors.New("conditional optional is not supported")
		case strings.HasPrefix(option, "default="):
			if strings.TrimSpace(strings.TrimPrefix(option, "default=")) == "" {
				return errors.New("default value is empty")
			}
		case strings.HasPrefix(option, "options="):
			if len(parseGoZeroAPIOptions(strings.TrimSpace(strings.TrimPrefix(option, "options=")))) == 0 {
				return errors.New("options value is empty")
			}
		case strings.HasPrefix(option, "range="):
			if err := validateGoZeroAPINumberRange(strings.TrimPrefix(option, "range=")); err != nil {
				return err
			}
		}
	}
	return nil
}

func splitGoZeroAPITagParts(raw string) ([]string, error) {
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
				return nil, errors.New("unbalanced modifier delimiters")
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
		return nil, errors.New("unbalanced modifier delimiters")
	}
	parts = append(parts, strings.TrimSpace(part.String()))
	return parts, nil
}

func parseGoZeroAPIOptions(raw string) []string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
		if raw == "" {
			return nil
		}
		return strings.FieldsFunc(raw, func(char rune) bool { return char == ',' })
	}
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "|")
}

func validateGoZeroAPINumberRange(raw string) error {
	if len(raw) < 3 || (raw[0] != '[' && raw[0] != '(') || (raw[len(raw)-1] != ']' && raw[len(raw)-1] != ')') {
		return fmt.Errorf("invalid range %q", raw)
	}
	leftText, rightText, ok := strings.Cut(raw[1:len(raw)-1], ":")
	if !ok || (strings.TrimSpace(leftText) == "" && strings.TrimSpace(rightText) == "") {
		return fmt.Errorf("invalid range %q", raw)
	}
	left, leftSet, err := parseGoZeroAPIRangeBound(leftText)
	if err != nil {
		return fmt.Errorf("invalid range %q", raw)
	}
	right, rightSet, err := parseGoZeroAPIRangeBound(rightText)
	if err != nil || leftSet && rightSet && (left > right || left == right && (raw[0] != '[' || raw[len(raw)-1] != ']')) {
		return fmt.Errorf("invalid range %q", raw)
	}
	return nil
}

func parseGoZeroAPIRangeBound(raw string) (float64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	return value, true, err
}

func validateAPIMethod(
	method IDLMethod,
	types map[string]IDLMessage,
	routes map[string]struct{},
	handlers map[string]struct{},
) []string {
	issues := []string{}
	methodName := exportName(method.Name)
	if strings.TrimSpace(method.HTTPMethod) == "" || strings.TrimSpace(method.HTTPPath) == "" {
		issues = append(issues, fmt.Sprintf("route %s is incomplete", methodName))
	} else {
		issues = append(issues, validateAPIPathParams(methodName, method.HTTPPath)...)
		routeKey := strings.ToUpper(method.HTTPMethod) + " " + method.HTTPPath
		if _, ok := routes[routeKey]; ok {
			issues = append(issues, fmt.Sprintf("duplicate route %s", routeKey))
		} else {
			routes[routeKey] = struct{}{}
		}
	}

	if method.Handler != "" {
		handlerName := exportName(method.Handler)
		if _, ok := handlers[handlerName]; ok {
			issues = append(issues, fmt.Sprintf("duplicate handler %s", handlerName))
		} else {
			handlers[handlerName] = struct{}{}
		}
	}

	if method.Request != "" && !apiMessageExists(method.Request, types) {
		issues = append(issues, fmt.Sprintf("route %s references unknown request type %s", methodName, method.Request))
	}
	if method.Response == "" {
		issues = append(issues, fmt.Sprintf("route %s response type is required", methodName))
	} else if !apiMessageExists(method.Response, types) {
		issues = append(issues, fmt.Sprintf("route %s references unknown response type %s", methodName, method.Response))
	}
	issues = append(issues, validateAPIDocResponses(method)...)
	return issues
}

func validateAPIDocResponses(method IDLMethod) []string {
	if len(method.Doc) == 0 {
		return nil
	}
	issues := []string{}
	if raw := strings.TrimSpace(method.Doc["respcode"]); raw != "" {
		if _, ok := apiResponseStatusCode(raw); !ok {
			issues = append(issues, fmt.Sprintf("route %s has invalid response status code %q", exportName(method.Name), raw))
		}
	}
	for _, item := range strings.Split(method.Doc["responses"], "<br>") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		codeText, _, ok := strings.Cut(item, "-")
		if !ok {
			issues = append(issues, fmt.Sprintf("route %s has invalid response description %q", exportName(method.Name), item))
			continue
		}
		if _, ok := apiResponseStatusCode(strings.TrimSpace(codeText)); !ok {
			issues = append(issues, fmt.Sprintf("route %s has invalid response status code %q", exportName(method.Name), strings.TrimSpace(codeText)))
		}
	}
	return issues
}

func apiResponseStatusCode(raw string) (int, bool) {
	statusCode, err := strconv.Atoi(strings.Trim(strings.TrimSpace(raw), "\"'"))
	if err != nil || statusCode < http.StatusContinue || statusCode > 599 {
		return 0, false
	}
	return statusCode, true
}

func validateAPIPathParams(methodName, path string) []string {
	var issues []string
	for _, raw := range rawAPIPathParamNames(path) {
		name := normalizeAPIPathParamName(raw)
		if name == "" {
			continue
		}
		if !apiPathParamNamePattern.MatchString(name) {
			issues = append(issues, fmt.Sprintf("route %s has invalid path parameter %q", methodName, raw))
		}
	}
	return issues
}

func rawAPIPathParamNames(path string) []string {
	var names []string
	for {
		start := strings.Index(path, "{")
		if start < 0 {
			return names
		}
		path = path[start+1:]
		end := strings.Index(path, "}")
		if end < 0 {
			return names
		}
		name := strings.TrimSpace(path[:end])
		if name != "" {
			names = append(names, name)
		}
		path = path[end+1:]
	}
}

func apiMessageExists(name string, types map[string]IDLMessage) bool {
	_, ok := types[exportName(name)]
	return ok
}

func apiBaseType(name string) string {
	name = strings.TrimSpace(name)
	for strings.HasPrefix(name, "[]") {
		name = strings.TrimSpace(strings.TrimPrefix(name, "[]"))
	}
	name = strings.TrimPrefix(name, "*")
	return name
}

func isAPIBuiltinType(name string) bool {
	_, ok := apiBuiltinTypes[name]
	return ok
}
