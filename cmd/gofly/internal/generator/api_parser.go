package generator

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

var (
	apiTypeRE        = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	apiServiceRE     = regexp.MustCompile(`^service\s+([A-Za-z_][A-Za-z0-9_-]*)\s*\{`)
	apiFieldRE       = regexp.MustCompile("^([A-Za-z_][A-Za-z0-9_]*)\\s+((?:\\[\\])?[A-Za-z_][A-Za-z0-9_]*)(?:\\s+(`[^`]*`))?$")
	apiInlineFieldRE = regexp.MustCompile("^(\\*?[A-Za-z_][A-Za-z0-9_]*)(?:\\s+(`[^`]*`))?$")
	apiRouteRE       = regexp.MustCompile(`^(get|post|put|patch|delete)\s+([^\s]+)\s*(?:\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\))?\s*returns\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)$`)
	apiDocValueRE    = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*[:=]\s*(?:"([^"]*)"|'([^']*)'|([^\s)]+))`)
)

func ParseAPI(content string) (IDLDocument, error) {
	doc := IDLDocument{Kind: "api"}
	scanner := bufio.NewScanner(strings.NewReader(stripBlockComments(content)))
	var currentMessage *IDLMessage
	var currentService *IDLService
	var handler string
	var methodDoc map[string]string
	var pendingServer IDLServerAnnotation
	var serverAnnotation strings.Builder
	var docAnnotation strings.Builder
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripLineComment(scanner.Text()))
		if line == "" {
			continue
		}
		if docAnnotation.Len() > 0 {
			docAnnotation.WriteByte(' ')
			docAnnotation.WriteString(line)
			if strings.Contains(line, ")") {
				methodDoc = parseAPIDocAnnotation(docAnnotation.String())
				docAnnotation.Reset()
			}
			continue
		}
		if serverAnnotation.Len() > 0 {
			serverAnnotation.WriteByte(' ')
			serverAnnotation.WriteString(line)
			if strings.Contains(line, ")") {
				annotation := parseAPIServerAnnotation(serverAnnotation.String())
				if currentService != nil {
					currentService.Server = mergeAPIServerAnnotation(currentService.Server, annotation)
				} else {
					pendingServer = mergeAPIServerAnnotation(pendingServer, annotation)
				}
				serverAnnotation.Reset()
			}
			continue
		}
		if currentMessage != nil {
			if line == "}" || line == "};" {
				doc.Messages = append(doc.Messages, *currentMessage)
				currentMessage = nil
				continue
			}
			if match := apiFieldRE.FindStringSubmatch(line); match != nil {
				currentMessage.Fields = append(currentMessage.Fields, IDLField{Name: match[1], Type: strings.TrimSpace(match[2]), Tag: strings.Trim(match[3], "`")})
				continue
			}
			if match := apiInlineFieldRE.FindStringSubmatch(line); match != nil {
				typeName := strings.TrimSpace(match[1])
				currentMessage.Fields = append(currentMessage.Fields, IDLField{
					Name:   strings.TrimPrefix(typeName, "*"),
					Type:   typeName,
					Tag:    strings.Trim(match[2], "`"),
					Inline: true,
				})
				continue
			}
			return IDLDocument{}, fmt.Errorf("parse api line %d: invalid field", lineNo)
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
			if strings.HasPrefix(line, "@doc") {
				if strings.Contains(line, "(") && !strings.Contains(line, ")") {
					docAnnotation.WriteString(line)
					continue
				}
				methodDoc = parseAPIDocAnnotation(line)
				continue
			}
			if strings.HasPrefix(line, "@server") {
				if strings.Contains(line, "(") && !strings.Contains(line, ")") {
					serverAnnotation.WriteString(line)
					continue
				}
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
				Doc:        methodDoc,
			})
			handler = ""
			methodDoc = nil
			continue
		}
		if strings.HasPrefix(line, "syntax") || strings.HasPrefix(line, "import") {
			continue
		}
		if strings.HasPrefix(line, "@server") {
			if strings.Contains(line, "(") && !strings.Contains(line, ")") {
				serverAnnotation.WriteString(line)
				continue
			}
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
	if serverAnnotation.Len() > 0 {
		return IDLDocument{}, fmt.Errorf("parse api: server annotation is not closed")
	}
	if docAnnotation.Len() > 0 {
		return IDLDocument{}, fmt.Errorf("parse api: doc annotation is not closed")
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
	middleware := values["middleware"]
	if middleware == "" {
		middleware = values["middlewares"]
	}
	return IDLServerAnnotation{
		Group:      values["group"],
		Prefix:     values["prefix"],
		JWT:        values["jwt"],
		Middleware: splitAPIAnnotationList(middleware),
		Values:     values,
	}
}

func parseAPIDocAnnotation(line string) map[string]string {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "@doc"))
	values := map[string]string{}
	for _, match := range apiDocValueRE.FindAllStringSubmatch(strings.Trim(line, "()"), -1) {
		value := match[2]
		if value == "" {
			value = match[3]
		}
		if value == "" {
			value = match[4]
		}
		values[strings.ToLower(match[1])] = strings.TrimSpace(value)
	}
	return values
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
