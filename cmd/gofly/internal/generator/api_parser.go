package generator

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	apiTypeRE          = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:struct\s*)?\{`)
	apiGroupedTypeRE   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(?:struct\s*)?\{`)
	apiTypeAliasRE     = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(?:=\s*)?(.+)$`)
	apiGroupedAliasRE  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s+(?:=\s*)?(.+)$`)
	apiSyntaxRE        = regexp.MustCompile(`^syntax\s*=\s*["']([^"']+)["']`)
	apiServiceRE       = regexp.MustCompile(`^service\s+([A-Za-z_][A-Za-z0-9_-]*)\s*\{`)
	apiInlineFieldRE   = regexp.MustCompile("^(\\*?[A-Za-z_][A-Za-z0-9_]*)(?:\\s+(`[^`]*`))?$")
	apiRouteRE         = regexp.MustCompile(`^(get|post|put|patch|delete)\s+([^\s]+)(?:\s+\(\s*([A-Za-z_][A-Za-z0-9_]*)?\s*\))?(?:\s+returns\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)?\s*\))?$`)
	apiDocValueRE      = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*[:=]\s*(?:"([^"]*)"|'([^']*)'|([^\s)]+))`)
	apiAnnotationKeyRE = regexp.MustCompile(`(?:^|\s+)([A-Za-z_][A-Za-z0-9_]*)\s*[:=]\s*`)
)

func ParseAPI(content string) (IDLDocument, error) {
	doc := IDLDocument{Kind: "api"}
	scanner := bufio.NewScanner(strings.NewReader(stripBlockComments(content)))
	var currentMessage *IDLMessage
	var currentService *IDLService
	var handler string
	var methodDoc map[string]string
	var pendingServer IDLServerAnnotation
	var pendingRoute IDLRouteAnnotation
	var serverAnnotation strings.Builder
	var docAnnotation strings.Builder
	var inImportBlock bool
	var inTypeBlock bool
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
					pendingRoute = mergeAPIRouteAnnotation(pendingRoute, annotation)
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
			if field, ok, err := parseAPIField(line); err != nil {
				return IDLDocument{}, fmt.Errorf("parse api line %d: %w", lineNo, err)
			} else if ok {
				currentMessage.Fields = append(currentMessage.Fields, field)
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
				pendingRoute = mergeAPIRouteAnnotation(pendingRoute, parseAPIServerAnnotation(line))
				continue
			}
			match := apiRouteRE.FindStringSubmatch(strings.ToLower(line[:min(len(line), 6)]) + line[min(len(line), 6):])
			if match == nil {
				return IDLDocument{}, fmt.Errorf("parse api line %d: invalid route", lineNo)
			}
			routeHandler := handler
			if pendingRoute.Handler != "" {
				routeHandler = pendingRoute.Handler
			}
			currentService.Methods = append(currentService.Methods, IDLMethod{
				Name:       handlerName(routeHandler, match[1], match[2]),
				Request:    match[3],
				Response:   match[4],
				HTTPMethod: strings.ToUpper(match[1]),
				HTTPPath:   match[2],
				Handler:    routeHandler,
				Doc:        methodDoc,
				Route:      pendingRoute,
			})
			handler = ""
			methodDoc = nil
			pendingRoute = IDLRouteAnnotation{}
			continue
		}
		if match := apiSyntaxRE.FindStringSubmatch(line); match != nil {
			doc.Syntax = match[1]
			continue
		}
		if inImportBlock {
			if line == ")" {
				inImportBlock = false
			}
			continue
		}
		if isAPIBlockStart(line, "import") {
			inImportBlock = true
			continue
		}
		if strings.HasPrefix(line, "import") {
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
		if inTypeBlock {
			if line == ")" {
				inTypeBlock = false
				continue
			}
			if match := apiGroupedTypeRE.FindStringSubmatch(line); match != nil {
				currentMessage = &IDLMessage{Name: match[1]}
				continue
			}
			if match := apiGroupedAliasRE.FindStringSubmatch(line); match != nil {
				return IDLDocument{}, fmt.Errorf("unsupported api type alias %s", match[1])
			}
			return IDLDocument{}, fmt.Errorf("parse api line %d: invalid grouped type", lineNo)
		}
		if isAPIBlockStart(line, "type") {
			inTypeBlock = true
			continue
		}
		if match := apiTypeRE.FindStringSubmatch(line); match != nil {
			currentMessage = &IDLMessage{Name: match[1]}
			continue
		}
		if match := apiTypeAliasRE.FindStringSubmatch(line); match != nil {
			return IDLDocument{}, fmt.Errorf("unsupported api type alias %s", match[1])
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
	if inImportBlock {
		return IDLDocument{}, fmt.Errorf("parse api: import block is not closed")
	}
	if inTypeBlock {
		return IDLDocument{}, fmt.Errorf("parse api: type block is not closed")
	}
	if len(doc.Services) == 0 && len(doc.Messages) == 0 {
		return IDLDocument{}, fmt.Errorf("parse api: no type or service found")
	}
	return doc, nil
}

func isAPIBlockStart(line, keyword string) bool {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), keyword))
	return line == "("
}

func parseAPIField(line string) (IDLField, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return IDLField{}, false, nil
	}
	if match := apiInlineFieldRE.FindStringSubmatch(line); match != nil {
		typeName := strings.TrimSpace(match[1])
		return IDLField{Name: strings.TrimPrefix(typeName, "*"), Type: typeName, Tag: strings.Trim(match[2], "`"), Inline: true}, true, nil
	}
	nameEnd := strings.IndexAny(line, " \t")
	if nameEnd <= 0 {
		return IDLField{}, false, nil
	}
	name := strings.TrimSpace(line[:nameEnd])
	if !isAPIIdentifier(name) {
		return IDLField{}, false, nil
	}
	remainder := strings.TrimSpace(line[nameEnd:])
	rawType, rawTag, err := splitAPIFieldTypeAndTag(remainder)
	if err != nil {
		return IDLField{}, false, err
	}
	ref, err := parseAPITypeRef(rawType)
	if err != nil {
		return IDLField{}, false, err
	}
	return IDLField{Name: name, Type: ref.String(), Tag: rawTag}, true, nil
}

func splitAPIFieldTypeAndTag(value string) (string, string, error) {
	if !strings.Contains(value, "`") {
		return strings.TrimSpace(value), "", nil
	}
	start := strings.IndexByte(value, '`')
	end := strings.LastIndexByte(value, '`')
	if start < 0 || end == start || strings.TrimSpace(value[end+1:]) != "" {
		return "", "", errors.New("invalid field tag")
	}
	return strings.TrimSpace(value[:start]), value[start+1 : end], nil
}

func isAPIIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if index == 0 && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && char != '_' {
			return false
		}
		if index > 0 && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
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

func mergeAPIRouteAnnotation(base IDLRouteAnnotation, annotation IDLServerAnnotation) IDLRouteAnnotation {
	if handler := strings.TrimSpace(annotation.Values["handler"]); handler != "" {
		base.Handler = handler
	}
	if annotation.Group != "" {
		base.Group = annotation.Group
	}
	if len(annotation.Values) > 0 {
		if base.Values == nil {
			base.Values = make(map[string]string, len(annotation.Values))
		}
		for key, value := range annotation.Values {
			base.Values[key] = value
		}
	}
	return base
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
	line = strings.TrimSpace(strings.Trim(line, "()"))
	matches := apiAnnotationKeyRE.FindAllStringSubmatchIndex(line, -1)
	for index, match := range matches {
		valueEnd := len(line)
		if index+1 < len(matches) {
			valueEnd = matches[index+1][0]
		}
		key := strings.ToLower(line[match[2]:match[3]])
		value := parseAPIAnnotationValue(line[match[1]:valueEnd])
		out[key] = value
	}
	return out
}

func parseAPIAnnotationValue(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) > 1 && (value[0] == '"' || value[0] == 39) {
		if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
			return value[1 : end+1]
		}
	}
	return strings.Trim(value, `,"'`)
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
