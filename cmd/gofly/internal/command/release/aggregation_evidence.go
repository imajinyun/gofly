package release

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/imajinyun/gofly/gateway"
	"github.com/imajinyun/gofly/rest"
)

type gatewayAggregationSARIFContext struct {
	Route         string
	OpenAPIPath   string
	OpenAPIMethod string
}

type gatewayAggregationChangeView struct {
	Kind     string                           `json:"kind"`
	Scope    string                           `json:"scope"`
	Source   string                           `json:"source,omitempty"`
	Target   string                           `json:"target,omitempty"`
	Severity string                           `json:"severity"`
	Message  string                           `json:"message"`
	Location gatewayAggregationChangeLocation `json:"location,omitempty"`
}

type gatewayAggregationChangeLocation struct {
	Route         string `json:"route,omitempty"`
	Path          string `json:"path,omitempty"`
	Method        string `json:"method,omitempty"`
	Step          string `json:"step,omitempty"`
	Mapping       string `json:"mapping,omitempty"`
	MappingSource string `json:"mappingSource,omitempty"`
	MappingTarget string `json:"mappingTarget,omitempty"`
}

type gatewayAggregationReportView struct {
	OK         bool                           `json:"ok"`
	Compatible bool                           `json:"compatible"`
	Errors     []string                       `json:"errors,omitempty"`
	Changes    []gatewayAggregationChangeView `json:"changes,omitempty"`
	Current    *gateway.AggregationConfig     `json:"current,omitempty"`
	Candidate  gateway.AggregationConfig      `json:"candidate"`
}

func gatewayAggregationValidationView(report gateway.AggregationValidationReport, context gatewayAggregationSARIFContext) gatewayAggregationReportView {
	view := gatewayAggregationReportView{
		OK:         report.OK,
		Compatible: report.Compatible,
		Errors:     report.Errors,
		Current:    report.Current,
		Candidate:  report.Candidate,
		Changes:    make([]gatewayAggregationChangeView, 0, len(report.Changes)),
	}
	for _, change := range report.Changes {
		view.Changes = append(view.Changes, gatewayAggregationChangeView{
			Kind:     change.Kind,
			Scope:    change.Scope,
			Source:   change.Source,
			Target:   change.Target,
			Severity: change.Severity,
			Message:  change.Message,
			Location: buildGatewayAggregationChangeLocation(context, change),
		})
	}
	return view
}

func buildGatewayAggregationChangeLocation(context gatewayAggregationSARIFContext, change gateway.TranscodeProfileChange) gatewayAggregationChangeLocation {
	location := gatewayAggregationChangeLocation{
		Route:  context.Route,
		Path:   context.OpenAPIPath,
		Method: context.OpenAPIMethod,
		Step:   gatewayAggregationChangeStep(change),
	}
	if gatewayAggregationChangeHasMapping(change) {
		location.MappingSource = change.Source
		location.MappingTarget = change.Target
		location.Mapping = gatewayAggregationMappingID(change.Source, change.Target)
	}
	return location
}

func gatewayAggregationChangeStep(change gateway.TranscodeProfileChange) string {
	if step := aggregationStepFromScope(change.Scope); step != "" {
		return step
	}
	if change.Scope == "aggregation_step" {
		return strings.TrimSpace(change.Source)
	}
	return ""
}

func gatewayAggregationMappingID(source, target string) string {
	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	switch {
	case source != "" && target != "":
		return source + " -> " + target
	case source != "":
		return source
	case target != "":
		return target
	default:
		return ""
	}
}

func gatewayAggregationChangeHasMapping(change gateway.TranscodeProfileChange) bool {
	return change.Scope == "aggregation_shape" ||
		strings.HasPrefix(change.Scope, "aggregation_request_query/") ||
		strings.HasPrefix(change.Scope, "aggregation_request_header/") ||
		strings.HasPrefix(change.Scope, "aggregation_request_body/")
}

func aggregationStepFromScope(scope string) string {
	for _, prefix := range []string{
		"aggregation_request_header/",
		"aggregation_request_query/",
		"aggregation_request_body/",
		"aggregation_request_required/",
		"aggregation_request_body_template/",
	} {
		if strings.HasPrefix(scope, prefix) {
			return strings.TrimPrefix(scope, prefix)
		}
	}
	return ""
}

func readGatewayOpenAPIDocument(path string) (rest.OpenAPIDocument, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- release check reads a generated OpenAPI file from a temporary project directory it just created.
	if err != nil {
		return rest.OpenAPIDocument{}, fmt.Errorf("read openapi document: %w", err)
	}
	var doc rest.OpenAPIDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return rest.OpenAPIDocument{}, fmt.Errorf("decode openapi document: %w", err)
	}
	return doc, nil
}

func gatewayAggregationFromRoutes(routes []gateway.RouteConfig, routeName string) (gateway.AggregationConfig, error) {
	routeName = strings.TrimSpace(routeName)
	for _, route := range routes {
		routeID := strings.TrimSpace(route.Method) + " " + strings.TrimSpace(route.PathPrefix)
		if routeName == "" || routeName == route.Name || routeName == routeID {
			if route.Aggregation.Enabled || len(route.Aggregation.Steps) > 0 || len(route.Aggregation.Shape.Mappings) > 0 || strings.TrimSpace(route.Aggregation.Shape.Mode) != "" {
				return route.Aggregation, nil
			}
		}
	}
	return gateway.AggregationConfig{}, fmt.Errorf("openapi aggregation route %q not found", routeName)
}

func gatewayOpenAPIAggregationSARIFContext(doc rest.OpenAPIDocument, routeName string) gatewayAggregationSARIFContext {
	routeName = strings.TrimSpace(routeName)
	for path, methods := range doc.Paths {
		for method, op := range methods {
			httpMethod := strings.ToUpper(strings.TrimSpace(method))
			staticPrefix := "/" + strings.Trim(strings.Split(strings.Trim(path, "/"), "/")[0], "{}")
			if staticPrefix == "/" {
				staticPrefix = path
			}
			candidateNames := []string{
				strings.TrimSpace(op.OperationID),
				httpMethod + " " + gatewayAggregationCleanPrefix(staticPrefix),
			}
			for _, candidate := range candidateNames {
				if routeName == "" || routeName == candidate {
					return gatewayAggregationSARIFContext{
						Route:         candidate,
						OpenAPIPath:   path,
						OpenAPIMethod: strings.ToUpper(method),
					}
				}
			}
		}
	}
	return gatewayAggregationSARIFContext{Route: routeName}
}

func gatewayAggregationCleanPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}
