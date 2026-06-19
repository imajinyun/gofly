package generator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var apiPathParamNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
		for _, method := range svc.Methods {
			issues = append(issues, validateAPIMethod(method, types, routes, handlers)...)
		}
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
		fieldType := apiBaseType(field.Type)
		if !isAPIBuiltinType(fieldType) {
			if _, ok := types[exportName(fieldType)]; !ok {
				issues = append(issues, fmt.Sprintf("unknown field type %s.%s %s", messageName, fieldName, field.Type))
			}
		}
	}
	return issues
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
	return issues
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
	return name
}

func isAPIBuiltinType(name string) bool {
	_, ok := apiBuiltinTypes[name]
	return ok
}
