// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// OpenAPIInfo describes the API metadata.
type OpenAPIInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// OpenAPIDocument is a simplified OpenAPI 3.0 document.
type OpenAPIDocument struct {
	OpenAPI    string                          `json:"openapi"`
	Info       OpenAPIInfo                     `json:"info"`
	Paths      map[string]map[string]Operation `json:"paths"`
	Components *Components                     `json:"components,omitempty"`
}

// Operation describes a single API operation.
type Operation struct {
	Summary     string              `json:"summary,omitempty"`
	Description string              `json:"description,omitempty"`
	OperationID string              `json:"operationId,omitempty"`
	Tags        []string            `json:"tags,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses"`
}

// Response describes an operation response.
type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
	Headers     map[string]Header    `json:"headers,omitempty"`
	Schema      *Schema              `json:"-"`
}

// Parameter describes an operation parameter.
type Parameter struct {
	Name        string  `json:"name"`
	In          string  `json:"in"`
	Description string  `json:"description,omitempty"`
	Required    bool    `json:"required,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
}

type RequestBody struct {
	Description string               `json:"description,omitempty"`
	Required    bool                 `json:"required,omitempty"`
	Content     map[string]MediaType `json:"content"`
}

type MediaType struct {
	Schema *Schema `json:"schema,omitempty"`
}

type Header struct {
	Description string  `json:"description,omitempty"`
	Schema      *Schema `json:"schema,omitempty"`
}

type Components struct {
	Schemas map[string]Schema `json:"schemas,omitempty"`
}

type Schema struct {
	Ref                  string            `json:"$ref,omitempty"`
	Type                 string            `json:"type,omitempty"`
	Format               string            `json:"format,omitempty"`
	Items                *Schema           `json:"items,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Required             []string          `json:"required,omitempty"`
	AdditionalProperties *Schema           `json:"additionalProperties,omitempty"`
	Enum                 []string          `json:"enum,omitempty"`
	Minimum              *float64          `json:"minimum,omitempty"`
	Maximum              *float64          `json:"maximum,omitempty"`
	MinLength            *int              `json:"minLength,omitempty"`
	MaxLength            *int              `json:"maxLength,omitempty"`
	MinItems             *int              `json:"minItems,omitempty"`
	MaxItems             *int              `json:"maxItems,omitempty"`
}

type RouteSpec struct {
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Summary     string              `json:"summary,omitempty"`
	Description string              `json:"description,omitempty"`
	OperationID string              `json:"operationId,omitempty"`
	Tags        []string            `json:"tags,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses,omitempty"`
}

func (s *Server) Routes() []RouteSpec {
	if s == nil {
		return nil
	}
	out := make([]RouteSpec, 0, len(s.routes))
	for _, route := range s.routes {
		if isBuiltInHealthRoute(route) {
			continue
		}
		out = append(out, route)
	}
	for i := range out {
		out[i].Tags = append([]string(nil), out[i].Tags...)
		out[i].Parameters = cloneParameters(out[i].Parameters)
		out[i].RequestBody = cloneRequestBody(out[i].RequestBody)
		out[i].Responses = cloneResponses(out[i].Responses)
	}
	return out
}

func (s *Server) OpenAPI(info OpenAPIInfo) OpenAPIDocument {
	if info.Title == "" {
		if s != nil {
			info.Title = s.conf.Name
		}
		if info.Title == "" {
			info.Title = "gofly service"
		}
	}
	if info.Version == "" {
		info.Version = "0.0.0"
	}
	doc := OpenAPIDocument{OpenAPI: "3.0.3", Info: info, Paths: make(map[string]map[string]Operation)}
	if s == nil {
		return doc
	}
	for _, route := range s.routes {
		if isBuiltInHealthRoute(route) {
			continue
		}
		method := strings.ToLower(route.Method)
		if method == "" {
			method = strings.ToLower(http.MethodGet)
		}
		if doc.Paths[route.Path] == nil {
			doc.Paths[route.Path] = make(map[string]Operation)
		}
		responses := cloneResponses(route.Responses)
		if len(responses) == 0 {
			responses = map[string]Response{"200": {Description: "OK"}}
		}
		parameters := mergePathParameters(route.Path, route.Parameters)
		doc.Paths[route.Path][method] = Operation{
			Summary:     route.Summary,
			Description: route.Description,
			OperationID: route.OperationID,
			Tags:        append([]string(nil), route.Tags...),
			Parameters:  parameters,
			RequestBody: cloneRequestBody(route.RequestBody),
			Responses:   responses,
		}
	}
	return doc
}

func isBuiltInHealthRoute(route RouteSpec) bool {
	if route.Method != http.MethodGet {
		return false
	}
	switch route.Path {
	case "/startupz", "/healthz", "/readyz", "/metrics", "/metrics.json":
		return true
	default:
		return false
	}
}

func routeSpecFromRoute(route Route) RouteSpec {
	return RouteSpec{
		Method:      strings.ToUpper(route.Method),
		Path:        route.Path,
		Summary:     route.Summary,
		Description: route.Description,
		OperationID: route.OperationID,
		Tags:        append([]string(nil), route.Tags...),
		Parameters:  cloneParameters(route.Parameters),
		RequestBody: cloneRequestBody(route.RequestBody),
		Responses:   cloneResponses(route.Responses),
	}
}

func JSONBodySchema(schema Schema, required bool) *RequestBody {
	return &RequestBody{Required: required, Content: map[string]MediaType{"application/json": {Schema: cloneSchemaPtr(&schema)}}}
}

// StructSchema derives a JSON OpenAPI schema from exported struct fields and
// gofly validate tags. It intentionally covers the portable subset supported
// by the built-in binder: required, min, max, and oneof.
func StructSchema(value any) Schema {
	typeOf := reflect.TypeOf(value)
	for typeOf != nil && typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	if typeOf == nil || typeOf.Kind() != reflect.Struct {
		return Schema{Type: "object"}
	}
	return schemaFromStructType(typeOf)
}

func JSONResponse(description string, schema Schema) Response {
	if description == "" {
		description = "OK"
	}
	return Response{Description: description, Content: map[string]MediaType{"application/json": {Schema: cloneSchemaPtr(&schema)}}}
}

// JSONErrorResponse documents gofly's standard REST error envelope.
func JSONErrorResponse(description string) Response {
	if description == "" {
		description = "Error"
	}
	return JSONResponse(description, ErrorResponseSchema())
}

// ErrorResponseSchema returns the OpenAPI schema for WriteError responses.
func ErrorResponseSchema() Schema {
	return StructSchema(ErrorResponse{})
}

// DefaultErrorResponses returns the stable machine-readable REST error responses
// most generated handlers should expose alongside success responses.
func DefaultErrorResponses() map[string]Response {
	return map[string]Response{
		"400": JSONErrorResponse("Invalid request"),
		"500": JSONErrorResponse("Internal server error"),
	}
}

func StringSchema() *Schema { return &Schema{Type: "string"} }

func IntegerSchema() *Schema { return &Schema{Type: "integer", Format: "int64"} }

func BooleanSchema() *Schema { return &Schema{Type: "boolean"} }

func NumberSchema() *Schema { return &Schema{Type: "number", Format: "double"} }

func ArraySchema(item Schema) *Schema { return &Schema{Type: "array", Items: cloneSchemaPtr(&item)} }

func RefSchema(ref string) *Schema { return &Schema{Ref: ref} }

func schemaFromStructType(typeOf reflect.Type) Schema {
	schema := Schema{Type: "object", Properties: map[string]Schema{}}
	for i := 0; i < typeOf.NumField(); i++ {
		field := typeOf.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if field.Anonymous && indirectType(field.Type).Kind() == reflect.Struct {
			embedded := schemaFromStructType(indirectType(field.Type))
			for name, property := range embedded.Properties {
				schema.Properties[name] = property
			}
			schema.Required = append(schema.Required, embedded.Required...)
			continue
		}
		name, ok := jsonFieldName(field)
		if !ok {
			continue
		}
		property := schemaFromType(field.Type)
		applyValidationRules(&property, field.Tag.Get("validate"))
		schema.Properties[name] = property
		if hasValidationRule(field.Tag.Get("validate"), "required") {
			schema.Required = append(schema.Required, name)
		}
	}
	if len(schema.Properties) == 0 {
		schema.Properties = nil
	}
	return schema
}

func schemaFromType(typeOf reflect.Type) Schema {
	typeOf = indirectType(typeOf)
	switch typeOf.Kind() {
	case reflect.Bool:
		return Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Schema{Type: "integer", Format: "int64"}
	case reflect.Float32, reflect.Float64:
		return Schema{Type: "number", Format: "double"}
	case reflect.Slice, reflect.Array:
		item := schemaFromType(typeOf.Elem())
		return Schema{Type: "array", Items: cloneSchemaPtr(&item)}
	case reflect.Struct:
		return schemaFromStructType(typeOf)
	case reflect.Map:
		additional := Schema{Type: "object"}
		if typeOf.Elem() != nil {
			additional = schemaFromType(typeOf.Elem())
		}
		return Schema{Type: "object", AdditionalProperties: cloneSchemaPtr(&additional)}
	default:
		return Schema{Type: "string"}
	}
}

func indirectType(typeOf reflect.Type) reflect.Type {
	for typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	return typeOf
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		name = field.Name
	}
	return name, true
}

func applyValidationRules(schema *Schema, tag string) {
	for _, rule := range strings.Split(tag, ",") {
		rule = strings.TrimSpace(rule)
		switch {
		case strings.HasPrefix(rule, "min="):
			applyMinRule(schema, strings.TrimPrefix(rule, "min="))
		case strings.HasPrefix(rule, "max="):
			applyMaxRule(schema, strings.TrimPrefix(rule, "max="))
		case strings.HasPrefix(rule, "oneof="):
			schema.Enum = strings.Fields(strings.TrimPrefix(rule, "oneof="))
		}
	}
}

func applyMinRule(schema *Schema, raw string) {
	limit, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return
	}
	switch schema.Type {
	case "string":
		v := int(limit)
		schema.MinLength = &v
	case "array":
		v := int(limit)
		schema.MinItems = &v
	case "integer", "number":
		schema.Minimum = &limit
	}
}

func applyMaxRule(schema *Schema, raw string) {
	limit, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return
	}
	switch schema.Type {
	case "string":
		v := int(limit)
		schema.MaxLength = &v
	case "array":
		v := int(limit)
		schema.MaxItems = &v
	case "integer", "number":
		schema.Maximum = &limit
	}
}

func hasValidationRule(tag string, want string) bool {
	for _, rule := range strings.Split(tag, ",") {
		if strings.TrimSpace(rule) == want {
			return true
		}
	}
	return false
}

func mergePathParameters(path string, parameters []Parameter) []Parameter {
	out := cloneParameters(parameters)
	seen := make(map[string]struct{}, len(out))
	for _, parameter := range out {
		seen[parameter.In+":"+parameter.Name] = struct{}{}
	}
	for _, name := range pathParamNames(path) {
		key := "path:" + name
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, Parameter{Name: name, In: "path", Required: true, Schema: StringSchema()})
	}
	return out
}

func pathParamNames(path string) []string {
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
		name := normalizePathParamName(strings.TrimSpace(path[:end]))
		if name != "" {
			names = append(names, name)
		}
		path = path[end+1:]
	}
}

func normalizePathParamName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "...")
	if name == "path" {
		return ""
	}
	return name
}

func cloneParameters(values []Parameter) []Parameter {
	if len(values) == 0 {
		return nil
	}
	out := make([]Parameter, len(values))
	for i, value := range values {
		out[i] = value
		out[i].Schema = cloneSchemaPtr(value.Schema)
	}
	return out
}

func cloneRequestBody(value *RequestBody) *RequestBody {
	if value == nil {
		return nil
	}
	out := *value
	out.Content = cloneContent(value.Content)
	return &out
}

func cloneResponses(values map[string]Response) map[string]Response {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]Response, len(values))
	for key, value := range values {
		value.Content = cloneContent(value.Content)
		value.Headers = cloneHeaders(value.Headers)
		value.Schema = cloneSchemaPtr(value.Schema)
		out[key] = value
	}
	return out
}

func cloneContent(values map[string]MediaType) map[string]MediaType {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]MediaType, len(values))
	for key, value := range values {
		value.Schema = cloneSchemaPtr(value.Schema)
		out[key] = value
	}
	return out
}

func cloneHeaders(values map[string]Header) map[string]Header {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]Header, len(values))
	for key, value := range values {
		value.Schema = cloneSchemaPtr(value.Schema)
		out[key] = value
	}
	return out
}

func cloneSchemaPtr(value *Schema) *Schema {
	if value == nil {
		return nil
	}
	out := cloneSchema(*value)
	return &out
}

func cloneSchema(value Schema) Schema {
	out := value
	out.Items = cloneSchemaPtr(value.Items)
	out.AdditionalProperties = cloneSchemaPtr(value.AdditionalProperties)
	if len(value.Properties) > 0 {
		out.Properties = make(map[string]Schema, len(value.Properties))
		for key, property := range value.Properties {
			out.Properties[key] = cloneSchema(property)
		}
	}
	out.Required = append([]string(nil), value.Required...)
	out.Enum = append([]string(nil), value.Enum...)
	out.Minimum = cloneFloat64Ptr(value.Minimum)
	out.Maximum = cloneFloat64Ptr(value.Maximum)
	out.MinLength = cloneIntPtr(value.MinLength)
	out.MaxLength = cloneIntPtr(value.MaxLength)
	out.MinItems = cloneIntPtr(value.MinItems)
	out.MaxItems = cloneIntPtr(value.MaxItems)
	return out
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
