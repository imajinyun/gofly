package rest

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestServerOpenAPIExportsRegisteredRoutes(t *testing.T) {
	s := MustNewServer(Config{Name: "orders"})
	s.AddRoute(Route{
		Method:      http.MethodPost,
		Path:        "/orders/{id}",
		Summary:     "Create order",
		Description: "Creates an order with a path id.",
		OperationID: "createOrder",
		Tags:        []string{"orders"},
		Parameters: []Parameter{{
			Name:        "trace",
			In:          "query",
			Description: "trace switch",
			Schema:      BooleanSchema(),
		}},
		RequestBody: JSONBodySchema(Schema{Type: "object", Properties: map[string]Schema{"name": {Type: "string"}}, Required: []string{"name"}}, true),
		Responses: map[string]Response{
			"201": JSONResponse("Created", Schema{Type: "object", Properties: map[string]Schema{"id": {Type: "string"}}, Required: []string{"id"}}),
		},
		Handler: func(ctx *Context) { ctx.String(http.StatusOK, "ok") },
	}, WithPrefix("/api/v1"))

	routes := s.Routes()
	if len(routes) != 1 || routes[0].Path != "/api/v1/orders/{id}" || routes[0].Method != http.MethodPost {
		t.Fatalf("routes = %+v, want prefixed POST route", routes)
	}
	routes[0].Tags[0] = "mutated"
	if s.Routes()[0].Tags[0] != "orders" {
		t.Fatal("Routes should return defensive copies of tags")
	}

	doc := s.OpenAPI(OpenAPIInfo{Version: "1.0.0"})
	if doc.OpenAPI != "3.0.3" || doc.Info.Title != "orders" || doc.Info.Version != "1.0.0" {
		t.Fatalf("openapi info = %+v", doc)
	}
	op := doc.Paths["/api/v1/orders/{id}"]["post"]
	if op.Summary != "Create order" || op.OperationID != "createOrder" || len(op.Tags) != 1 || op.Tags[0] != "orders" {
		t.Fatalf("operation = %+v, want exported route metadata", op)
	}
	if len(op.Parameters) != 2 || op.Parameters[0].Name != "trace" || op.Parameters[1].Name != "id" || !op.Parameters[1].Required {
		t.Fatalf("parameters = %+v, want query trace and path id", op.Parameters)
	}
	if op.RequestBody == nil || !op.RequestBody.Required || op.RequestBody.Content["application/json"].Schema.Properties["name"].Type != "string" {
		t.Fatalf("request body = %+v, want required json body schema", op.RequestBody)
	}
	if op.Responses["201"].Description != "Created" || op.Responses["201"].Content["application/json"].Schema.Properties["id"].Type != "string" {
		t.Fatalf("responses = %+v, want documented 201 response", op.Responses)
	}
	routes[0].Parameters[0].Schema.Type = "mutated"
	if s.Routes()[0].Parameters[0].Schema.Type != "boolean" {
		t.Fatal("Routes should return defensive copies of parameters")
	}
	if _, err := json.Marshal(doc); err != nil {
		t.Fatalf("marshal openapi doc: %v", err)
	}
}

func TestNilServerOpenAPI(t *testing.T) {
	var s *Server
	doc := s.OpenAPI(OpenAPIInfo{})
	if doc.Info.Title != "gofly service" || doc.Info.Version != "0.0.0" || len(doc.Paths) != 0 {
		t.Fatalf("nil server openapi = %+v, want default empty document", doc)
	}
}

func TestCloneHeadersDefensiveCopy_BitsUT(t *testing.T) {
	if got := cloneHeaders(nil); got != nil {
		t.Fatalf("cloneHeaders(nil) = %#v, want nil", got)
	}
	if got := cloneHeaders(map[string]Header{}); got != nil {
		t.Fatalf("cloneHeaders(empty) = %#v, want nil", got)
	}

	source := map[string]Header{
		"X-Trace": {
			Description: "trace id",
			Schema: &Schema{
				Type:       "string",
				Properties: map[string]Schema{"nested": {Type: "integer"}},
				Required:   []string{"nested"},
			},
		},
	}
	cloned := cloneHeaders(source)
	if cloned["X-Trace"].Schema == source["X-Trace"].Schema {
		t.Fatal("cloneHeaders reused schema pointer, want defensive copy")
	}
	source["X-Trace"].Schema.Type = "mutated"
	source["X-Trace"].Schema.Properties["nested"] = Schema{Type: "mutated"}
	source["X-Trace"].Schema.Required[0] = "mutated"
	gotSchema := cloned["X-Trace"].Schema
	if gotSchema.Type != "string" || gotSchema.Properties["nested"].Type != "integer" || gotSchema.Required[0] != "nested" {
		t.Fatalf("cloned schema = %#v, want unaffected deep copy", gotSchema)
	}
}

func TestOpenAPIPathParamNamesNormalizesCatchAll(t *testing.T) {
	got := pathParamNames("/files/{file...}/meta/{id}")
	want := []string{"file", "id"}
	if len(got) != len(want) {
		t.Fatalf("pathParamNames catch-all = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pathParamNames catch-all = %v, want %v", got, want)
		}
	}
	if got := pathParamNames("/{path...}"); len(got) != 0 {
		t.Fatalf("pathParamNames path catch-all = %v, want empty", got)
	}
}

func TestOpenAPISchemaHelpers(t *testing.T) {
	integer := IntegerSchema()
	if integer.Type != "integer" || integer.Format != "int64" {
		t.Fatalf("IntegerSchema() = %+v, want integer int64", integer)
	}

	number := NumberSchema()
	if number.Type != "number" || number.Format != "double" {
		t.Fatalf("NumberSchema() = %+v, want number double", number)
	}

	item := Schema{Type: "object", Properties: map[string]Schema{"id": {Type: "string"}}, Required: []string{"id"}}
	array := ArraySchema(item)
	if array.Type != "array" || array.Items == nil || array.Items.Properties["id"].Type != "string" || array.Items.Required[0] != "id" {
		t.Fatalf("ArraySchema() = %+v, want cloned object item schema", array)
	}
	item.Properties["id"] = Schema{Type: "mutated"}
	item.Required[0] = "mutated"
	if array.Items.Properties["id"].Type != "string" || array.Items.Required[0] != "id" {
		t.Fatalf("ArraySchema item = %+v, want defensive clone", array.Items)
	}

	ref := RefSchema("#/components/schemas/User")
	if ref.Ref != "#/components/schemas/User" || ref.Type != "" {
		t.Fatalf("RefSchema() = %+v, want ref-only schema", ref)
	}
}

func TestStructSchemaLinksValidationTags_BitsUT(t *testing.T) {
	type Embedded struct {
		Tenant string `json:"tenant" validate:"required"`
	}
	type createOrderRequest struct {
		Embedded
		SKU      string   `json:"sku" validate:"required,min=3,max=64"`
		Quantity int      `json:"quantity" validate:"required,min=1,max=100"`
		Status   string   `json:"status" validate:"oneof=pending paid canceled"`
		Labels   []string `json:"labels" validate:"min=1,max=3"`
		Ignored  string   `json:"-" validate:"required"`
	}

	schema := StructSchema(createOrderRequest{})
	if schema.Type != "object" {
		t.Fatalf("StructSchema type = %q, want object", schema.Type)
	}
	if len(schema.Required) != 3 || schema.Required[0] != "tenant" || schema.Required[1] != "sku" || schema.Required[2] != "quantity" {
		t.Fatalf("StructSchema required = %#v, want tenant/sku/quantity", schema.Required)
	}
	sku := schema.Properties["sku"]
	if sku.Type != "string" || sku.MinLength == nil || *sku.MinLength != 3 || sku.MaxLength == nil || *sku.MaxLength != 64 {
		t.Fatalf("sku schema = %#v, want string length bounds", sku)
	}
	quantity := schema.Properties["quantity"]
	if quantity.Type != "integer" || quantity.Minimum == nil || *quantity.Minimum != 1 || quantity.Maximum == nil || *quantity.Maximum != 100 {
		t.Fatalf("quantity schema = %#v, want integer numeric bounds", quantity)
	}
	status := schema.Properties["status"]
	if len(status.Enum) != 3 || status.Enum[0] != "pending" || status.Enum[2] != "canceled" {
		t.Fatalf("status enum = %#v, want pending/paid/canceled", status.Enum)
	}
	labels := schema.Properties["labels"]
	if labels.Type != "array" || labels.MinItems == nil || *labels.MinItems != 1 || labels.MaxItems == nil || *labels.MaxItems != 3 {
		t.Fatalf("labels schema = %#v, want array item bounds", labels)
	}
	if _, ok := schema.Properties["Ignored"]; ok {
		t.Fatalf("StructSchema included json:- field: %#v", schema.Properties)
	}
}

func TestDefaultErrorResponsesDocumentStableEnvelope_BitsUT(t *testing.T) {
	schema := ErrorResponseSchema()
	for _, name := range []string{"code", "text", "message", "status", "fields"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("ErrorResponseSchema properties = %#v, want %q", schema.Properties, name)
		}
	}
	fields := schema.Properties["fields"]
	if fields.Type != "array" || fields.Items == nil || fields.Items.Properties["field"].Type != "string" || fields.Items.Properties["rule"].Type != "string" {
		t.Fatalf("fields schema = %#v, want validation failure array", fields)
	}

	responses := DefaultErrorResponses()
	for _, code := range []string{"400", "500"} {
		resp := responses[code]
		if resp.Description == "" || resp.Content["application/json"].Schema.Properties["code"].Type != "string" {
			t.Fatalf("DefaultErrorResponses[%s] = %#v, want JSON ErrorResponse schema", code, resp)
		}
	}
	responses["400"] = Response{Description: "mutated"}
	if DefaultErrorResponses()["400"].Description == "mutated" {
		t.Fatal("DefaultErrorResponses reused mutable response map")
	}
}

func TestOpenAPIExportsDefaultErrorResponses_BitsUT(t *testing.T) {
	s := MustNewServer(Config{Name: "orders"})
	responses := DefaultErrorResponses()
	responses["200"] = JSONResponse("OK", StructSchema(struct {
		Message string `json:"message"`
	}{}))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/orders", Responses: responses, Handler: func(ctx *Context) { ctx.String(http.StatusOK, "ok") }})

	op := s.OpenAPI(OpenAPIInfo{}).Paths["/orders"]["get"]
	if op.Responses["400"].Content["application/json"].Schema.Properties["fields"].Type != "array" {
		t.Fatalf("400 response = %#v, want documented validation fields", op.Responses["400"])
	}
	responses["400"] = Response{Description: "mutated"}
	if s.OpenAPI(OpenAPIInfo{}).Paths["/orders"]["get"].Responses["400"].Description == "mutated" {
		t.Fatal("OpenAPI reused mutable route response map")
	}
}
