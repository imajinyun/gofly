package generator

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

var (
	apiTypeRE    = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	apiServiceRE = regexp.MustCompile(`^service\s+([A-Za-z_][A-Za-z0-9_-]*)\s*\{`)
	apiFieldRE   = regexp.MustCompile("^([A-Za-z_][A-Za-z0-9_]*)\\s+((?:\\[\\])?[A-Za-z_][A-Za-z0-9_]*)(?:\\s+(`[^`]*`))?$")
	apiRouteRE   = regexp.MustCompile(`^(get|post|put|patch|delete)\s+([^\s]+)\s*(?:\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\))?\s*returns\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)$`)
)

func ParseAPI(content string) (IDLDocument, error) {
	doc := IDLDocument{Kind: "api"}
	scanner := bufio.NewScanner(strings.NewReader(stripBlockComments(content)))
	var currentMessage *IDLMessage
	var currentService *IDLService
	var handler string
	var pendingServer IDLServerAnnotation
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripLineComment(scanner.Text()))
		if line == "" {
			continue
		}
		if currentMessage != nil {
			if line == "}" || line == "};" {
				doc.Messages = append(doc.Messages, *currentMessage)
				currentMessage = nil
				continue
			}
			match := apiFieldRE.FindStringSubmatch(line)
			if match == nil {
				return IDLDocument{}, fmt.Errorf("parse api line %d: invalid field", lineNo)
			}
			currentMessage.Fields = append(currentMessage.Fields, IDLField{Name: match[1], Type: strings.TrimSpace(match[2]), Tag: strings.Trim(match[3], "`")})
			continue
		}
		if currentService != nil {
			if line == "}" || line == "};" {
				doc.Services = append(doc.Services, *currentService)
				currentService = nil
				continue
			}
			if strings.HasPrefix(line, "@handler") {
				handler = strings.TrimSpace(strings.TrimPrefix(line, "@handler"))
				continue
			}
			if strings.HasPrefix(line, "@server") {
				currentService.Server = mergeAPIServerAnnotation(currentService.Server, parseAPIServerAnnotation(line))
				continue
			}
			match := apiRouteRE.FindStringSubmatch(strings.ToLower(line[:min(len(line), 6)]) + line[min(len(line), 6):])
			if match == nil {
				return IDLDocument{}, fmt.Errorf("parse api line %d: invalid route", lineNo)
			}
			currentService.Methods = append(currentService.Methods, IDLMethod{
				Name:       handlerName(handler, match[1], match[2]),
				Request:    match[3],
				Response:   match[4],
				HTTPMethod: strings.ToUpper(match[1]),
				HTTPPath:   match[2],
				Handler:    handler,
			})
			handler = ""
			continue
		}
		if strings.HasPrefix(line, "syntax") || strings.HasPrefix(line, "import") {
			continue
		}
		if strings.HasPrefix(line, "@server") {
			pendingServer = mergeAPIServerAnnotation(pendingServer, parseAPIServerAnnotation(line))
			continue
		}
		if match := apiTypeRE.FindStringSubmatch(line); match != nil {
			currentMessage = &IDLMessage{Name: match[1]}
			continue
		}
		if match := apiServiceRE.FindStringSubmatch(line); match != nil {
			currentService = &IDLService{Name: match[1], Server: pendingServer}
			pendingServer = IDLServerAnnotation{}
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return IDLDocument{}, fmt.Errorf("scan api: %w", err)
	}
	if currentMessage != nil {
		return IDLDocument{}, fmt.Errorf("parse api: type %s is not closed", currentMessage.Name)
	}
	if currentService != nil {
		return IDLDocument{}, fmt.Errorf("parse api: service %s is not closed", currentService.Name)
	}
	if len(doc.Services) == 0 && len(doc.Messages) == 0 {
		return IDLDocument{}, fmt.Errorf("parse api: no type or service found")
	}
	return doc, nil
}

func parseAPIServerAnnotation(line string) IDLServerAnnotation {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "@server"))
	line = strings.Trim(line, "()")
	values := parseAPIAnnotationValues(line)
	return IDLServerAnnotation{
		Group:      values["group"],
		Prefix:     values["prefix"],
		JWT:        values["jwt"],
		Middleware: splitAPIAnnotationList(values["middleware"]),
		Values:     values,
	}
}

func parseAPIAnnotationValues(line string) map[string]string {
	out := map[string]string{}
	tokens := strings.Fields(line)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		key, value, ok := strings.Cut(token, ":")
		if !ok {
			key, value, ok = strings.Cut(token, "=")
		}
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if value == "" && strings.HasSuffix(token, ":") && i+1 < len(tokens) {
			i++
			value = strings.Trim(strings.TrimSpace(tokens[i]), `"'`)
		}
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func splitAPIAnnotationList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func mergeAPIServerAnnotation(base IDLServerAnnotation, override IDLServerAnnotation) IDLServerAnnotation {
	if override.Group != "" {
		base.Group = override.Group
	}
	if override.Prefix != "" {
		base.Prefix = override.Prefix
	}
	if override.JWT != "" {
		base.JWT = override.JWT
	}
	if len(override.Middleware) > 0 {
		base.Middleware = append([]string(nil), override.Middleware...)
	}
	if len(override.Values) > 0 {
		if base.Values == nil {
			base.Values = map[string]string{}
		}
		for key, value := range override.Values {
			base.Values[key] = value
		}
	}
	return base
}

func handlerName(handler string, method string, path string) string {
	if handler != "" {
		return exportName(handler)
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == ':' || r == '-' || r == '_' })
	var name strings.Builder
	name.Grow(len(method) + len(path))
	name.WriteString(strings.ToLower(method))
	for _, part := range parts {
		name.WriteString(exportName(part))
	}
	return exportName(name.String())
}
