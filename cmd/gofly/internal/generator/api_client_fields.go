package generator

import "strings"

type apiClientFieldLocation string

const (
	apiClientFieldPath   apiClientFieldLocation = "path"
	apiClientFieldQuery  apiClientFieldLocation = "query"
	apiClientFieldHeader apiClientFieldLocation = "header"
	apiClientFieldBody   apiClientFieldLocation = "body"
)

const (
	apiTagPath   = "path"
	apiTagQuery  = "query"
	apiTagForm   = "form"
	apiTagHeader = "header"
	apiTagJSON   = "json"
)

type apiClientField struct {
	Field    IDLField
	Property string
	WireName string
	Location apiClientFieldLocation
}

func apiClientFields(method IDLMethod, request IDLMessage) []apiClientField {
	pathNames := make(map[string]struct{})
	for _, name := range openAPIPathParamNames(method.HTTPPath) {
		pathNames[strings.ToLower(name)] = struct{}{}
	}

	fields := make([]apiClientField, 0, len(request.Fields))
	for _, field := range request.Fields {
		property := apiClientPropertyName(field)
		if name, ok := apiFieldWireName(field, apiTagPath); ok {
			fields = append(fields, apiClientField{Field: field, Property: property, WireName: name, Location: apiClientFieldPath})
			continue
		}
		if _, ok := pathNames[strings.ToLower(property)]; ok {
			fields = append(fields, apiClientField{Field: field, Property: property, WireName: property, Location: apiClientFieldPath})
			continue
		}
		if name, ok := apiFieldWireName(field, apiTagHeader); ok {
			fields = append(fields, apiClientField{Field: field, Property: property, WireName: name, Location: apiClientFieldHeader})
			continue
		}
		if name, ok := apiFieldWireName(field, apiTagForm); ok {
			fields = append(fields, apiClientField{Field: field, Property: property, WireName: name, Location: apiClientFieldQuery})
			continue
		}
		if name, ok := apiFieldWireName(field, apiTagQuery); ok {
			fields = append(fields, apiClientField{Field: field, Property: property, WireName: name, Location: apiClientFieldQuery})
			continue
		}
		if raw, ok := lookupAPIStructTag(field.Tag, apiTagJSON); ok && strings.TrimSpace(strings.Split(raw, ",")[0]) == "-" {
			continue
		}
		if name, ok := apiFieldWireName(field, apiTagJSON); ok {
			fields = append(fields, apiClientField{Field: field, Property: property, WireName: name, Location: apiClientFieldBody})
			continue
		}
		location := apiClientFieldBody
		if strings.EqualFold(method.HTTPMethod, "GET") || strings.EqualFold(method.HTTPMethod, "DELETE") {
			location = apiClientFieldQuery
		}
		fields = append(fields, apiClientField{Field: field, Property: property, WireName: property, Location: location})
	}
	return fields
}

func apiClientPropertyName(field IDLField) string {
	for _, key := range []string{apiTagJSON, apiTagPath, apiTagForm, apiTagQuery} {
		if name, ok := apiFieldWireName(field, key); ok && isAPIClientIdentifier(name) {
			return name
		}
	}
	return lowerCamel(field.Name)
}

func isAPIClientIdentifier(name string) bool {
	for index, char := range name {
		if index == 0 {
			if char != '_' && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
				return false
			}
			continue
		}
		if char != '_' && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return name != ""
}

func apiFieldWireName(field IDLField, key string) (string, bool) {
	raw, ok := lookupAPIStructTag(field.Tag, key)
	if !ok {
		return "", false
	}
	parts, err := splitGoZeroAPITagParts(raw)
	if err != nil || len(parts) == 0 {
		return "", false
	}
	name := strings.TrimSpace(parts[0])
	if name == "-" {
		return "", false
	}
	if name == "" {
		name = lowerCamel(field.Name)
	}
	return name, true
}

func apiClientJSONWireName(field IDLField) string {
	if name, ok := apiFieldWireName(field, apiTagJSON); ok {
		return name
	}
	return apiClientPropertyName(field)
}

func apiClientFieldsAt(fields []apiClientField, location apiClientFieldLocation) []apiClientField {
	out := make([]apiClientField, 0, len(fields))
	for _, field := range fields {
		if field.Location == location {
			out = append(out, field)
		}
	}
	return out
}

func apiClientMethodAllowsBody(method string) bool {
	return !strings.EqualFold(method, "GET")
}

func apiClientBodyNeedsProjection(fields, bodyFields []apiClientField) bool {
	return len(fields) != len(bodyFields)
}

func apiClientFieldForPath(fields []apiClientField, name string) apiClientField {
	for _, field := range fields {
		if field.Location == apiClientFieldPath && strings.EqualFold(field.WireName, name) {
			return field
		}
	}
	return apiClientField{Property: lowerCamel(name), WireName: name, Location: apiClientFieldPath}
}
