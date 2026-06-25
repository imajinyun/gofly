package generator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v2"
)

type APIOptions struct {
	APIFile    string
	Dir        string
	Package    string
	RPCPackage string
	Test       bool
	TypeGroup  bool
	Profile    string
}

type RESTCodeOptions struct {
	Package    string
	RPCPackage string
}

type APIFormatOptions struct {
	APIFile string
	Dir     string
	Output  string
	Write   bool
}

type APIDocOptions struct {
	APIFile string
	Dir     string
	Output  string
	Format  string
}

type ProtoDocOptions struct {
	ProtoFile string
	Dir       string
	Output    string
	Format    string
}

type APIClientOptions struct {
	APIFile  string
	Dir      string
	Output   string
	Language string
	BaseURL  string
}

type APITypesOptions struct {
	APIFile string
	Dir     string
	Output  string
	Package string
}

type APIRouteOptions struct {
	APIFile string
	Dir     string
	Output  string
	Format  string
}

type APIImportOptions struct {
	Source  string
	Dir     string
	Output  string
	Service string
}

type APIDiffOptions struct {
	Base   string
	Target string
	Dir    string
	Output string
	Format string
}

type APIRouteInfo struct {
	Service     string   `json:"service"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Handler     string   `json:"handler"`
	Request     string   `json:"request,omitempty"`
	Response    string   `json:"response"`
	Group       string   `json:"group,omitempty"`
	Prefix      string   `json:"prefix,omitempty"`
	JWT         string   `json:"jwt,omitempty"`
	Middlewares []string `json:"middlewares,omitempty"`
}

type APIDiffResult struct {
	AddedRoutes   []APIRouteInfo      `json:"addedRoutes,omitempty"`
	RemovedRoutes []APIRouteInfo      `json:"removedRoutes,omitempty"`
	ChangedRoutes []APIRouteChange    `json:"changedRoutes,omitempty"`
	AddedTypes    []IDLMessage        `json:"addedTypes,omitempty"`
	RemovedTypes  []IDLMessage        `json:"removedTypes,omitempty"`
	ChangedTypes  []APITypeDiffChange `json:"changedTypes,omitempty"`
}

type APIRouteChange struct {
	Key    string       `json:"key"`
	Base   APIRouteInfo `json:"base"`
	Target APIRouteInfo `json:"target"`
}

type APITypeDiffChange struct {
	Name   string     `json:"name"`
	Base   IDLMessage `json:"base"`
	Target IDLMessage `json:"target"`
}

func GenerateRESTFromAPI(opts APIOptions) error {
	if opts.APIFile == "" {
		return errors.New("api file is required")
	}
	if opts.Dir == "" {
		opts.Dir = "."
	}
	content, err := os.ReadFile(opts.APIFile)
	if err != nil {
		return fmt.Errorf("read api file: %w", err)
	}
	doc, err := ParseAPI(string(content))
	if err != nil {
		return err
	}
	if _, err := normalizeGenerationProfile(opts.Profile); err != nil {
		return err
	}
	opts.Dir = filepath.Join(opts.Dir, "internal", "api")
	if strings.TrimSpace(opts.Package) == "" {
		opts.Package = filepath.Base(opts.Dir)
	}
	opts.Package = lowerName(opts.Package)
	return writeRESTFiles(doc, opts)
}

func FormatAPIFromFile(opts APIFormatOptions) ([]byte, error) {
	if opts.APIFile == "" && opts.Dir != "" {
		return formatAPIDir(opts)
	}
	if opts.APIFile == "" {
		return nil, errors.New("api file is required")
	}
	if opts.Dir != "" && opts.Output != "" {
		return nil, errors.New("api format output cannot be used with dir")
	}
	content, err := os.ReadFile(opts.APIFile)
	if err != nil {
		return nil, fmt.Errorf("read api file: %w", err)
	}
	doc, err := ParseAPI(string(content))
	if err != nil {
		return nil, err
	}
	formatted := FormatAPI(doc)
	if opts.Output != "" {
		if err := writeGeneratedFile(opts.Output, formatted); err != nil {
			return nil, fmt.Errorf("write formatted api file: %w", err)
		}
		return formatted, nil
	}
	if opts.Write {
		if err := writeGeneratedFile(opts.APIFile, formatted); err != nil {
			return nil, fmt.Errorf("write formatted api file: %w", err)
		}
	}
	return formatted, nil
}

func formatAPIDir(opts APIFormatOptions) ([]byte, error) {
	if opts.Output != "" {
		return nil, errors.New("api format output cannot be used with dir")
	}
	if opts.Dir == "" {
		return nil, errors.New("api format directory is required")
	}
	info, err := os.Stat(opts.Dir)
	if err != nil {
		return nil, fmt.Errorf("stat api format directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("api format path is not a directory: %s", opts.Dir)
	}
	var last []byte
	err = filepath.WalkDir(opts.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".api" {
			return nil
		}
		formatted, err := FormatAPIFromFile(APIFormatOptions{APIFile: path, Write: opts.Write})
		if err != nil {
			return err
		}
		last = formatted
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("format api directory: %w", err)
	}
	return last, nil
}

func FormatAPI(doc IDLDocument) []byte {
	var b bytes.Buffer
	for i, msg := range doc.Messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		fprintf(&b, "type %s {\n", exportName(msg.Name))
		for _, field := range msg.Fields {
			fprintf(&b, "  %s %s\n", exportName(field.Name), field.Type)
		}
		fprintf(&b, "}\n")
	}
	if len(doc.Messages) > 0 && len(doc.Services) > 0 {
		b.WriteByte('\n')
	}
	for i, svc := range doc.Services {
		if i > 0 {
			b.WriteByte('\n')
		}
		fprintf(&b, "service %s {\n", svc.Name)
		for _, method := range svc.Methods {
			if method.Handler != "" {
				fprintf(&b, "  @handler %s\n", exportName(method.Handler))
			}
			if method.Request == "" {
				fprintf(&b, "  %s %s returns (%s)\n", strings.ToLower(method.HTTPMethod), method.HTTPPath, exportName(method.Response))
				continue
			}
			fprintf(&b, "  %s %s (%s) returns (%s)\n", strings.ToLower(method.HTTPMethod), method.HTTPPath, exportName(method.Request), exportName(method.Response))
		}
		fprintf(&b, "}\n")
	}
	return b.Bytes()
}

func GenerateAPIFromOpenAPI(opts APIImportOptions) error {
	if opts.Source == "" {
		return errors.New("openapi source file is required")
	}
	content, err := os.ReadFile(opts.Source)
	if err != nil {
		return fmt.Errorf("read openapi file: %w", err)
	}
	doc, err := parseOpenAPIToIDL(content, opts.Service)
	if err != nil {
		return err
	}
	data := FormatAPI(doc)
	output := opts.Output
	if output == "" {
		if opts.Dir == "" {
			opts.Dir = "."
		}
		name := openAPIServiceName(opts.Service)
		if name == "api" {
			name = strings.TrimSuffix(filepath.Base(opts.Source), filepath.Ext(opts.Source))
			if name == "" {
				name = "service"
			}
		}
		output = filepath.Join(opts.Dir, name+".api")
	}
	if err := writeGeneratedFile(output, data); err != nil {
		return fmt.Errorf("write imported api file: %w", err)
	}
	return nil
}

func GenerateAPIDiff(opts APIDiffOptions) error {
	if opts.Base == "" {
		return errors.New("base api file is required")
	}
	if opts.Target == "" {
		return errors.New("target api file is required")
	}
	base, err := readAPIFile(opts.Base)
	if err != nil {
		return fmt.Errorf("read base api: %w", err)
	}
	target, err := readAPIFile(opts.Target)
	if err != nil {
		return fmt.Errorf("read target api: %w", err)
	}
	diff := DiffAPI(base, target)
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "text"
	}
	var data []byte
	switch format {
	case "text", "txt", "plain":
		data = formatAPIDiffText(diff)
	case "md", "markdown":
		data = formatAPIDiffMarkdown(diff)
	case "json":
		data, err = json.MarshalIndent(diff, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal api diff: %w", err)
		}
		data = append(data, '\n')
	default:
		return fmt.Errorf("unsupported api diff format %q", opts.Format)
	}
	output := opts.Output
	if output == "" {
		if opts.Dir == "" {
			opts.Dir = "."
		}
		name := strings.TrimSuffix(filepath.Base(opts.Target), filepath.Ext(opts.Target))
		ext := ".diff.txt"
		if format == "md" || format == "markdown" {
			ext = ".diff.md"
		}
		if format == "json" {
			ext = ".diff.json"
		}
		output = filepath.Join(opts.Dir, name+ext)
	}
	if err := writeGeneratedFile(output, data); err != nil {
		return fmt.Errorf("write api diff: %w", err)
	}
	return nil
}

func readAPIFile(path string) (IDLDocument, error) {
	// #nosec G304 -- API files are explicit generator inputs from CLI flags or caller options.
	content, err := os.ReadFile(path)
	if err != nil {
		return IDLDocument{}, err
	}
	doc, err := ParseAPI(string(content))
	if err != nil {
		return IDLDocument{}, err
	}
	return doc, nil
}

func DiffAPI(base, target IDLDocument) APIDiffResult {
	diff := APIDiffResult{}
	baseRoutes := apiRouteMap(apiRouteInfos(base))
	targetRoutes := apiRouteMap(apiRouteInfos(target))
	for _, key := range sortedRouteKeys(targetRoutes) {
		targetRoute := targetRoutes[key]
		baseRoute, ok := baseRoutes[key]
		if !ok {
			diff.AddedRoutes = append(diff.AddedRoutes, targetRoute)
			continue
		}
		if !apiRouteInfoEqual(baseRoute, targetRoute) {
			diff.ChangedRoutes = append(diff.ChangedRoutes, APIRouteChange{Key: key, Base: baseRoute, Target: targetRoute})
		}
	}
	for _, key := range sortedRouteKeys(baseRoutes) {
		if _, ok := targetRoutes[key]; !ok {
			diff.RemovedRoutes = append(diff.RemovedRoutes, baseRoutes[key])
		}
	}
	baseTypes := apiTypeMap(base.Messages)
	targetTypes := apiTypeMap(target.Messages)
	for _, name := range sortedIDLMessageNames(targetTypes) {
		targetType := targetTypes[name]
		baseType, ok := baseTypes[name]
		if !ok {
			diff.AddedTypes = append(diff.AddedTypes, targetType)
			continue
		}
		if apiMessageSignature(baseType) != apiMessageSignature(targetType) {
			diff.ChangedTypes = append(diff.ChangedTypes, APITypeDiffChange{Name: name, Base: baseType, Target: targetType})
		}
	}
	for _, name := range sortedIDLMessageNames(baseTypes) {
		if _, ok := targetTypes[name]; !ok {
			diff.RemovedTypes = append(diff.RemovedTypes, baseTypes[name])
		}
	}
	return diff
}

func apiRouteMap(routes []APIRouteInfo) map[string]APIRouteInfo {
	out := make(map[string]APIRouteInfo, len(routes))
	for _, route := range routes {
		out[apiRouteKey(route)] = route
	}
	return out
}

func apiRouteKey(route APIRouteInfo) string {
	parts := make([]string, 0, 3)
	if route.Service != "" {
		parts = append(parts, route.Service)
	}
	parts = append(parts, strings.ToUpper(route.Method), route.Path)
	return strings.Join(parts, " ")
}

func apiRouteInfoEqual(base APIRouteInfo, target APIRouteInfo) bool {
	return base.Service == target.Service &&
		base.Method == target.Method &&
		base.Path == target.Path &&
		base.Handler == target.Handler &&
		base.Request == target.Request &&
		base.Response == target.Response &&
		base.Group == target.Group &&
		base.Prefix == target.Prefix &&
		base.JWT == target.JWT &&
		reflect.DeepEqual(base.Middlewares, target.Middlewares)
}

func sortedRouteKeys(values map[string]APIRouteInfo) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func apiTypeMap(messages []IDLMessage) map[string]IDLMessage {
	out := make(map[string]IDLMessage, len(messages))
	for _, msg := range messages {
		out[exportName(msg.Name)] = msg
	}
	return out
}

func apiMessageSignature(msg IDLMessage) string {
	fields := make([]string, 0, len(msg.Fields))
	for _, field := range msg.Fields {
		fields = append(fields, exportName(field.Name)+":"+field.Type)
	}
	sort.Strings(fields)
	return strings.Join(fields, ";")
}

func formatAPIDiffText(diff APIDiffResult) []byte {
	var b bytes.Buffer
	if apiDiffEmpty(diff) {
		b.WriteString("No API changes\n")
		return b.Bytes()
	}
	writeAPIDiffTextRoutes(&b, "Added routes", diff.AddedRoutes)
	writeAPIDiffTextRoutes(&b, "Removed routes", diff.RemovedRoutes)
	if len(diff.ChangedRoutes) > 0 {
		fprintf(&b, "Changed routes\n")
		for _, change := range diff.ChangedRoutes {
			fprintf(
				&b,
				"  ~ %s: %s -> %s\n",
				change.Key,
				apiRouteSignature(change.Base),
				apiRouteSignature(change.Target),
			)
		}
	}
	writeAPIDiffTextTypes(&b, "Added types", diff.AddedTypes)
	writeAPIDiffTextTypes(&b, "Removed types", diff.RemovedTypes)
	if len(diff.ChangedTypes) > 0 {
		fprintf(&b, "Changed types\n")
		for _, change := range diff.ChangedTypes {
			fprintf(&b, "  ~ %s: %s -> %s\n", change.Name, apiMessageSignature(change.Base), apiMessageSignature(change.Target))
		}
	}
	return b.Bytes()
}

func writeAPIDiffTextRoutes(b *bytes.Buffer, title string, routes []APIRouteInfo) {
	if len(routes) == 0 {
		return
	}
	fprintf(b, "%s\n", title)
	prefix := apiDiffListPrefix(title)
	for _, route := range routes {
		fprintf(b, "  %s %s\n", prefix, apiRouteSignature(route))
	}
}

func writeAPIDiffTextTypes(b *bytes.Buffer, title string, messages []IDLMessage) {
	if len(messages) == 0 {
		return
	}
	fprintf(b, "%s\n", title)
	prefix := apiDiffListPrefix(title)
	for _, msg := range messages {
		fprintf(b, "  %s %s %s\n", prefix, exportName(msg.Name), apiMessageSignature(msg))
	}
}

func formatAPIDiffMarkdown(diff APIDiffResult) []byte {
	var b bytes.Buffer
	fprintf(&b, "# API Diff\n\n")
	if apiDiffEmpty(diff) {
		fprintf(&b, "No API changes.\n")
		return b.Bytes()
	}
	writeAPIDiffMarkdownRoutes(&b, "Added routes", diff.AddedRoutes)
	writeAPIDiffMarkdownRoutes(&b, "Removed routes", diff.RemovedRoutes)
	if len(diff.ChangedRoutes) > 0 {
		fprintf(&b, "## Changed routes\n\n| Route | Base | Target |\n| --- | --- | --- |\n")
		for _, change := range diff.ChangedRoutes {
			fprintf(
				&b,
				"| `%s` | `%s` | `%s` |\n",
				change.Key,
				apiRouteSignature(change.Base),
				apiRouteSignature(change.Target),
			)
		}
		b.WriteByte('\n')
	}
	writeAPIDiffMarkdownTypes(&b, "Added types", diff.AddedTypes)
	writeAPIDiffMarkdownTypes(&b, "Removed types", diff.RemovedTypes)
	if len(diff.ChangedTypes) > 0 {
		fprintf(&b, "## Changed types\n\n| Type | Base | Target |\n| --- | --- | --- |\n")
		for _, change := range diff.ChangedTypes {
			fprintf(
				&b,
				"| `%s` | `%s` | `%s` |\n",
				change.Name,
				apiMessageSignature(change.Base),
				apiMessageSignature(change.Target),
			)
		}
	}
	return b.Bytes()
}

func writeAPIDiffMarkdownRoutes(b *bytes.Buffer, title string, routes []APIRouteInfo) {
	if len(routes) == 0 {
		return
	}
	fprintf(b, "## %s\n\n| Method | Path | Request | Response |\n| --- | --- | --- | --- |\n", title)
	for _, route := range routes {
		fprintf(b, "| %s | `%s` | `%s` | `%s` |\n", route.Method, route.Path, apiEmptyDash(route.Request), route.Response)
	}
	b.WriteByte('\n')
}

func writeAPIDiffMarkdownTypes(b *bytes.Buffer, title string, messages []IDLMessage) {
	if len(messages) == 0 {
		return
	}
	fprintf(b, "## %s\n\n", title)
	for _, msg := range messages {
		fprintf(b, "- `%s` `%s`\n", exportName(msg.Name), apiMessageSignature(msg))
	}
	b.WriteByte('\n')
}

func apiRouteSignature(route APIRouteInfo) string {
	parts := make([]string, 0, 5)
	if route.Service != "" {
		parts = append(parts, route.Service)
	}
	parts = append(parts, strings.ToUpper(route.Method), route.Path)
	if route.Handler != "" {
		parts = append(parts, "handler="+route.Handler)
	}
	if route.Request != "" {
		parts = append(parts, "request="+route.Request)
	}
	if route.Response != "" {
		parts = append(parts, "response="+route.Response)
	}
	if route.Group != "" {
		parts = append(parts, "group="+route.Group)
	}
	if route.Prefix != "" {
		parts = append(parts, "prefix="+route.Prefix)
	}
	if route.JWT != "" {
		parts = append(parts, "jwt="+route.JWT)
	}
	if len(route.Middlewares) > 0 {
		parts = append(parts, "middlewares="+strings.Join(route.Middlewares, ","))
	}
	return strings.Join(parts, " ")
}

func apiEmptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func apiDiffListPrefix(title string) string {
	if strings.HasPrefix(strings.ToLower(title), "removed") {
		return "-"
	}
	return "+"
}

func apiDiffEmpty(diff APIDiffResult) bool {
	return len(diff.AddedRoutes) == 0 &&
		len(diff.RemovedRoutes) == 0 &&
		len(diff.ChangedRoutes) == 0 &&
		len(diff.AddedTypes) == 0 &&
		len(diff.RemovedTypes) == 0 &&
		len(diff.ChangedTypes) == 0
}

func GenerateAPIDoc(opts APIDocOptions) error {
	if opts.APIFile == "" {
		return errors.New("api file is required")
	}
	content, err := os.ReadFile(opts.APIFile)
	if err != nil {
		return fmt.Errorf("read api file: %w", err)
	}
	doc, err := ParseAPI(string(content))
	if err != nil {
		return err
	}
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "markdown"
	}
	var data []byte
	switch format {
	case "md", "markdown":
		data = generateAPIMarkdown(doc)
	case "openapi", "openapi3", "oas3", "swagger", "json":
		data, err = generateAPIOpenAPI(doc)
		if err != nil {
			return err
		}
	case "yaml", "yml", "openapi-yaml", "swagger-yaml":
		data, err = generateAPIOpenAPIYAML(doc)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported api doc format %q", opts.Format)
	}
	output := opts.Output
	if output == "" {
		if opts.Dir == "" {
			opts.Dir = "."
		}
		ext := apiDocExt(format)
		name := strings.TrimSuffix(filepath.Base(opts.APIFile), filepath.Ext(opts.APIFile))
		output = filepath.Join(opts.Dir, name+ext)
	}
	if err := writeGeneratedFile(output, data); err != nil {
		return fmt.Errorf("write api doc: %w", err)
	}
	return nil
}

func GenerateProtoDoc(opts ProtoDocOptions) error {
	if opts.ProtoFile == "" {
		return errors.New("proto file is required")
	}
	content, err := os.ReadFile(opts.ProtoFile)
	if err != nil {
		return fmt.Errorf("read proto file: %w", err)
	}
	doc, err := ParseProto(string(content))
	if err != nil {
		return err
	}
	doc = protoDocWithTranscodingDefaults(doc)
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "openapi"
	}
	var data []byte
	switch format {
	case "openapi", "openapi3", "oas3", "swagger", "json":
		data, err = generateAPIOpenAPI(doc)
		if err != nil {
			return err
		}
	case "yaml", "yml", "openapi-yaml", "swagger-yaml":
		data, err = generateAPIOpenAPIYAML(doc)
		if err != nil {
			return err
		}
	case "md", "markdown":
		data = generateAPIMarkdown(doc)
	default:
		return fmt.Errorf("unsupported proto doc format %q", opts.Format)
	}
	output := opts.Output
	if output == "" {
		if opts.Dir == "" {
			opts.Dir = "."
		}
		name := strings.TrimSuffix(filepath.Base(opts.ProtoFile), filepath.Ext(opts.ProtoFile))
		output = filepath.Join(opts.Dir, name+apiDocExt(format))
	}
	if err := writeGeneratedFile(output, data); err != nil {
		return fmt.Errorf("write proto doc: %w", err)
	}
	return nil
}

func protoDocWithTranscodingDefaults(doc IDLDocument) IDLDocument {
	for svcIndex := range doc.Services {
		serviceName := serviceFullName(doc, doc.Services[svcIndex].Name)
		for methodIndex := range doc.Services[svcIndex].Methods {
			method := &doc.Services[svcIndex].Methods[methodIndex]
			if method.ClientStream || method.ServerStream {
				continue
			}
			if strings.TrimSpace(method.HTTPMethod) == "" {
				method.HTTPMethod = "POST"
			}
			if strings.TrimSpace(method.HTTPPath) == "" {
				method.HTTPPath = "/" + serviceName + "/" + method.Name
			}
		}
	}
	return doc
}

func apiDocExt(format string) string {
	switch format {
	case "openapi", "openapi3", "oas3", "swagger", "json":
		return ".json"
	case "yaml", "yml", "openapi-yaml", "swagger-yaml":
		return ".yaml"
	default:
		return ".md"
	}
}

func GenerateAPIClient(opts APIClientOptions) error {
	if opts.APIFile == "" {
		return errors.New("api file is required")
	}
	content, err := os.ReadFile(opts.APIFile)
	if err != nil {
		return fmt.Errorf("read api file: %w", err)
	}
	doc, err := ParseAPI(string(content))
	if err != nil {
		return err
	}
	language := strings.ToLower(strings.TrimSpace(opts.Language))
	if language == "" {
		language = "typescript"
	}
	var data []byte
	var ext string
	switch language {
	case "ts", "typescript":
		data = generateTypeScriptClient(doc, opts.BaseURL)
		ext = ".ts"
	case "js", "javascript":
		data = generateJavaScriptClient(doc, opts.BaseURL)
		ext = ".js"
	case "dart":
		data = generateDartClient(doc, opts.BaseURL)
		ext = ".dart"
	case "java":
		data = generateJavaClient(doc, opts.BaseURL)
		ext = ".java"
	case "kt", "kotlin":
		data = generateKotlinClient(doc, opts.BaseURL)
		ext = ".kt"
	default:
		return fmt.Errorf("unsupported api client language %q", opts.Language)
	}
	output := opts.Output
	if output == "" {
		if opts.Dir == "" {
			opts.Dir = "."
		}
		name := strings.TrimSuffix(filepath.Base(opts.APIFile), filepath.Ext(opts.APIFile))
		if language == "java" {
			output = filepath.Join(opts.Dir, "APIClient.java")
		} else if language == "kotlin" || language == "kt" {
			output = filepath.Join(opts.Dir, "APIClient.kt")
		} else {
			output = filepath.Join(opts.Dir, name+"_client"+ext)
		}
	}
	if err := writeGeneratedFile(output, data); err != nil {
		return fmt.Errorf("write api client: %w", err)
	}
	return nil
}

func GenerateAPITypes(opts APITypesOptions) error {
	if opts.APIFile == "" {
		return errors.New("api file is required")
	}
	content, err := os.ReadFile(opts.APIFile)
	if err != nil {
		return fmt.Errorf("read api file: %w", err)
	}
	doc, err := ParseAPI(string(content))
	if err != nil {
		return err
	}
	if len(doc.Messages) == 0 {
		return errors.New("api type is required")
	}
	pkg := lowerName(opts.Package)
	if pkg == "" {
		pkg = "types"
	}
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", pkg)
	for _, msg := range doc.Messages {
		writeAPIMessage(&b, msg)
	}
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format api types file: %w", err)
	}
	output := opts.Output
	if output == "" {
		if opts.Dir == "" {
			opts.Dir = "."
		}
		output = filepath.Join(opts.Dir, "types.go")
	}
	if err := writeGeneratedFile(output, formatted); err != nil {
		return fmt.Errorf("write api types: %w", err)
	}
	return nil
}

func GenerateAPIRoutes(opts APIRouteOptions) error {
	if opts.APIFile == "" {
		return errors.New("api file is required")
	}
	content, err := os.ReadFile(opts.APIFile)
	if err != nil {
		return fmt.Errorf("read api file: %w", err)
	}
	doc, err := ParseAPI(string(content))
	if err != nil {
		return err
	}
	routes := apiRouteInfos(doc)
	if len(routes) == 0 {
		return errors.New("api route is required")
	}
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "text"
	}
	var data []byte
	switch format {
	case "text", "txt", "plain":
		data = generateAPIRoutesText(routes)
	case "md", "markdown":
		data = generateAPIRoutesMarkdown(routes)
	case "json":
		// #nosec G117 -- APIRouteInfo.JWT is goctl @server auth policy metadata, not a bearer token or secret.
		data, err = json.MarshalIndent(routes, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal api routes: %w", err)
		}
		data = append(data, '\n')
	default:
		return fmt.Errorf("unsupported api route format %q", opts.Format)
	}
	output := opts.Output
	if output == "" {
		if opts.Dir == "" {
			opts.Dir = "."
		}
		name := strings.TrimSuffix(filepath.Base(opts.APIFile), filepath.Ext(opts.APIFile))
		ext := ".routes.txt"
		if format == "md" || format == "markdown" {
			ext = ".routes.md"
		}
		if format == "json" {
			ext = ".routes.json"
		}
		output = filepath.Join(opts.Dir, name+ext)
	}
	if err := writeGeneratedFile(output, data); err != nil {
		return fmt.Errorf("write api routes: %w", err)
	}
	return nil
}

func apiRouteInfos(doc IDLDocument) []APIRouteInfo {
	routes := make([]APIRouteInfo, 0)
	for _, svc := range doc.Services {
		for _, method := range svc.Methods {
			routes = append(routes, APIRouteInfo{
				Service:     svc.Name,
				Method:      method.HTTPMethod,
				Path:        openAPIServicePath(svc, method.HTTPPath),
				Handler:     exportName(method.Name),
				Request:     exportOptionalName(method.Request),
				Response:    exportName(method.Response),
				Group:       exportAnnotationName(svc.Server.Group),
				Prefix:      svc.Server.Prefix,
				JWT:         exportAnnotationName(svc.Server.JWT),
				Middlewares: exportAnnotationNames(svc.Server.Middleware),
			})
		}
	}
	return routes
}

func exportOptionalName(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return exportName(name)
}

func exportAnnotationName(name string) string {
	return exportOptionalName(name)
}

func exportAnnotationNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		exported := exportAnnotationName(name)
		if exported != "" {
			out = append(out, exported)
		}
	}
	return out
}

func generateAPIRoutesText(routes []APIRouteInfo) []byte {
	var b bytes.Buffer
	fprintf(&b, "METHOD\tPATH\tHANDLER\tREQUEST\tRESPONSE\tSERVICE\n")
	for _, route := range routes {
		fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n", route.Method, route.Path, route.Handler, route.Request, route.Response, route.Service)
	}
	return b.Bytes()
}

func generateAPIRoutesMarkdown(routes []APIRouteInfo) []byte {
	var b bytes.Buffer
	fprintf(&b, "# API Routes\n\n")
	fprintf(&b, "| Method | Path | Handler | Request | Response | Service |\n")
	fprintf(&b, "| --- | --- | --- | --- | --- | --- |\n")
	for _, route := range routes {
		fprintf(&b, "| %s | `%s` | `%s` | `%s` | `%s` | `%s` |\n", route.Method, route.Path, route.Handler, route.Request, route.Response, route.Service)
	}
	return b.Bytes()
}

type openAPIDocument struct {
	OpenAPI     string                       `json:"openapi" yaml:"openapi"`
	Swagger     string                       `json:"swagger" yaml:"swagger"`
	Info        openAPIInfo                  `json:"info" yaml:"info"`
	Paths       map[string]openAPIPathItem   `json:"paths" yaml:"paths"`
	Components  openAPIComponents            `json:"components" yaml:"components"`
	Definitions map[string]openAPISpecSchema `json:"definitions" yaml:"definitions"`
	Parameters  map[string]openAPIParameter  `json:"parameters" yaml:"parameters"`
}

type openAPIInfo struct {
	Title string `json:"title" yaml:"title"`
}

type openAPIComponents struct {
	Schemas    map[string]openAPISpecSchema `json:"schemas" yaml:"schemas"`
	Parameters map[string]openAPIParameter  `json:"parameters" yaml:"parameters"`
}

type openAPIPathItem map[string]openAPIOperation

const openAPIPathParametersKey = "__path_parameters"

func (item *openAPIPathItem) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := openAPIPathItem{}
	for method, payload := range raw {
		method = strings.ToLower(method)
		if method == "parameters" {
			var params []openAPIParameter
			if err := json.Unmarshal(payload, &params); err != nil {
				return fmt.Errorf("parse openapi path parameters: %w", err)
			}
			out[openAPIPathParametersKey] = openAPIOperation{Parameters: params}
			continue
		}
		if !isHTTPMethod(method) {
			continue
		}
		var operation openAPIOperation
		if err := json.Unmarshal(payload, &operation); err != nil {
			return fmt.Errorf("parse openapi %s operation: %w", method, err)
		}
		out[method] = operation
	}
	*item = out
	return nil
}

func (item *openAPIPathItem) UnmarshalYAML(unmarshal func(any) error) error {
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	out := openAPIPathItem{}
	for method, payload := range raw {
		method = strings.ToLower(method)
		data, err := yaml.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal openapi %s path item: %w", method, err)
		}
		if method == "parameters" {
			var params []openAPIParameter
			if err := yaml.Unmarshal(data, &params); err != nil {
				return fmt.Errorf("parse openapi path parameters: %w", err)
			}
			out[openAPIPathParametersKey] = openAPIOperation{Parameters: params}
			continue
		}
		if !isHTTPMethod(method) {
			continue
		}
		var operation openAPIOperation
		if err := yaml.Unmarshal(data, &operation); err != nil {
			return fmt.Errorf("parse openapi %s operation: %w", method, err)
		}
		out[method] = operation
	}
	*item = out
	return nil
}

type openAPIOperation struct {
	OperationID string                     `json:"operationId" yaml:"operationId"`
	Tags        []string                   `json:"tags" yaml:"tags"`
	Parameters  []openAPIParameter         `json:"parameters" yaml:"parameters"`
	RequestBody *openAPIRequestBody        `json:"requestBody" yaml:"requestBody"`
	Responses   map[string]openAPIResponse `json:"responses" yaml:"responses"`
}

type openAPIParameter struct {
	Ref      string            `json:"$ref" yaml:"$ref"`
	Name     string            `json:"name" yaml:"name"`
	In       string            `json:"in" yaml:"in"`
	Required bool              `json:"required" yaml:"required"`
	Schema   openAPISpecSchema `json:"schema" yaml:"schema"`
}

type openAPIRequestBody struct {
	Content map[string]openAPIMediaType `json:"content" yaml:"content"`
}

type openAPIResponse struct {
	Content map[string]openAPIMediaType `json:"content" yaml:"content"`
}

type openAPIMediaType struct {
	Schema openAPISpecSchema `json:"schema" yaml:"schema"`
}

type openAPISpecSchema struct {
	Ref        string                       `json:"$ref" yaml:"$ref"`
	Type       string                       `json:"type" yaml:"type"`
	Format     string                       `json:"format" yaml:"format"`
	Properties map[string]openAPISpecSchema `json:"properties" yaml:"properties"`
	Items      *openAPISpecSchema           `json:"items" yaml:"items"`
}

func parseOpenAPIToIDL(content []byte, serviceName string) (IDLDocument, error) {
	var spec openAPIDocument
	if err := unmarshalOpenAPI(content, &spec); err != nil {
		return IDLDocument{}, err
	}
	if len(spec.Paths) == 0 {
		return IDLDocument{}, errors.New("openapi path is required")
	}
	if serviceName == "" {
		serviceName = spec.Info.Title
	}
	if serviceName == "" {
		serviceName = "imported-api"
	}
	components := spec.Components.Schemas
	if len(components) == 0 {
		components = spec.Definitions
	}
	parameterComponents := spec.Components.Parameters
	if len(parameterComponents) == 0 {
		parameterComponents = spec.Parameters
	}
	doc := IDLDocument{Kind: "api", Services: []IDLService{{Name: openAPIServiceName(serviceName)}}}
	messageByName := map[string]IDLMessage{}
	for _, name := range sortedSchemaNames(components) {
		msg := openAPISchemaToMessage(name, components[name], components)
		if len(msg.Fields) == 0 {
			continue
		}
		messageByName[exportName(msg.Name)] = msg
	}
	paths := sortedOpenAPIPaths(spec.Paths)
	for _, path := range paths {
		item := spec.Paths[path]
		pathParams := openAPIPathItemParameters(item, parameterComponents)
		for _, method := range sortedOpenAPIMethods(item) {
			operation := item[method]
			if !isHTTPMethod(method) {
				continue
			}
			operation.Parameters = openAPIJoinParameters(
				pathParams,
				openAPIResolveParameters(operation.Parameters, parameterComponents),
			)
			handler := openAPIOperationName(method, path, operation)
			request := openAPIRequestName(handler, operation)
			if msg, ok := openAPIRequestMessage(request, operation, components); ok {
				messageByName[exportName(msg.Name)] = msg
			}
			response := openAPIResponseName(handler, operation)
			if schema, ok := openAPIResponseSchema(operation); ok && response != "" && schema.Ref == "" && len(schema.Properties) > 0 {
				messageByName[response] = openAPISchemaToMessage(response, schema, components)
			}
			if response == "" {
				response = "EmptyResp"
				if _, ok := messageByName[response]; !ok {
					messageByName[response] = IDLMessage{Name: response}
				}
			}
			doc.Services[0].Methods = append(doc.Services[0].Methods, IDLMethod{
				Name:       handler,
				Handler:    handler,
				Request:    request,
				Response:   response,
				HTTPMethod: strings.ToUpper(method),
				HTTPPath:   path,
			})
		}
	}
	if len(doc.Services[0].Methods) == 0 {
		return IDLDocument{}, errors.New("openapi operation is required")
	}
	for _, name := range sortedIDLMessageNames(messageByName) {
		doc.Messages = append(doc.Messages, messageByName[name])
	}
	return doc, nil
}

func unmarshalOpenAPI(content []byte, spec *openAPIDocument) error {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return errors.New("openapi source file is empty")
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, spec); err != nil {
			return fmt.Errorf("parse openapi json: %w", err)
		}
		return nil
	}
	if err := yaml.Unmarshal(trimmed, spec); err != nil {
		return fmt.Errorf("parse openapi yaml: %w", err)
	}
	return nil
}

func sortedSchemaNames(values map[string]openAPISpecSchema) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedOpenAPIPaths(values map[string]openAPIPathItem) []string {
	paths := make([]string, 0, len(values))
	for path := range values {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func sortedOpenAPIMethods(item openAPIPathItem) []string {
	methods := make([]string, 0, len(item))
	for method := range item {
		methods = append(methods, strings.ToLower(method))
	}
	sort.Strings(methods)
	return methods
}

func sortedIDLMessageNames(values map[string]IDLMessage) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isHTTPMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "post", "put", "patch", "delete":
		return true
	default:
		return false
	}
}

func openAPIServiceName(name string) string {
	name = strings.Join(strings.Fields(name), "-")
	return lowerSnake(name)
}

func openAPISchemaToMessage(name string, schema openAPISpecSchema, components map[string]openAPISpecSchema) IDLMessage {
	if schema.Ref != "" {
		name = openAPIRefName(schema.Ref)
		schema = components[name]
	}
	msg := IDLMessage{Name: exportName(name)}
	for _, fieldName := range sortedSchemaNames(schema.Properties) {
		field := schema.Properties[fieldName]
		msg.Fields = append(msg.Fields, IDLField{Name: exportName(fieldName), Type: openAPISchemaType(field, components)})
	}
	return msg
}

func openAPISchemaType(schema openAPISpecSchema, components map[string]openAPISpecSchema) string {
	if schema.Ref != "" {
		return exportName(openAPIRefName(schema.Ref))
	}
	switch strings.ToLower(schema.Type) {
	case "array":
		if schema.Items == nil {
			return "[]string"
		}
		return "[]" + openAPISchemaType(*schema.Items, components)
	case "integer":
		if schema.Format == "int32" {
			return "int"
		}
		return "int64"
	case "number":
		if schema.Format == "float" {
			return "float32"
		}
		return "float64"
	case "boolean":
		return "bool"
	case "object":
		return "string"
	default:
		return "string"
	}
}

func openAPIRefName(ref string) string {
	ref = strings.TrimSpace(ref)
	if idx := strings.LastIndex(ref, "/"); idx >= 0 {
		ref = ref[idx+1:]
	}
	return ref
}

func openAPIOperationName(method string, path string, operation openAPIOperation) string {
	if operation.OperationID != "" {
		return exportName(operation.OperationID)
	}
	return handlerName("", method, path)
}

func openAPIRequestName(handler string, operation openAPIOperation) string {
	hasParams := hasOpenAPIRequestParams(operation.Parameters)
	if schema, ok := openAPIRequestSchema(operation); ok {
		if hasParams {
			return exportName(handler) + "Req"
		}
		if schema.Ref != "" {
			return exportName(openAPIRefName(schema.Ref))
		}
		if len(schema.Properties) > 0 {
			return exportName(handler) + "Req"
		}
	}
	if hasParams {
		return exportName(handler) + "Req"
	}
	return ""
}

func openAPIResponseName(handler string, operation openAPIOperation) string {
	schema, ok := openAPIResponseSchema(operation)
	if !ok {
		return ""
	}
	if schema.Ref != "" {
		return exportName(openAPIRefName(schema.Ref))
	}
	if len(schema.Properties) > 0 {
		return exportName(handler) + "Resp"
	}
	return ""
}

func openAPIRequestSchema(operation openAPIOperation) (openAPISpecSchema, bool) {
	if operation.RequestBody == nil {
		return openAPISpecSchema{}, false
	}
	return openAPIMediaSchema(operation.RequestBody.Content)
}

func openAPIResponseSchema(operation openAPIOperation) (openAPISpecSchema, bool) {
	for _, code := range []string{"200", "201", "202", "default"} {
		response, ok := operation.Responses[code]
		if !ok {
			continue
		}
		if schema, ok := openAPIMediaSchema(response.Content); ok {
			return schema, true
		}
	}
	return openAPISpecSchema{}, false
}

func openAPIMediaSchema(content map[string]openAPIMediaType) (openAPISpecSchema, bool) {
	if len(content) == 0 {
		return openAPISpecSchema{}, false
	}
	for _, key := range []string{"application/json", "application/*+json", "*/*"} {
		media, ok := content[key]
		if ok {
			return media.Schema, true
		}
	}
	keys := make([]string, 0, len(content))
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return content[keys[0]].Schema, true
}

func hasOpenAPIRequestParams(params []openAPIParameter) bool {
	for _, param := range params {
		if param.In == "path" || param.In == "query" {
			return true
		}
	}
	return false
}

func openAPIRequestMessage(name string, operation openAPIOperation, components map[string]openAPISpecSchema) (IDLMessage, bool) {
	if name == "" {
		return IDLMessage{}, false
	}
	paramsSchema := openAPIParamsSchema(operation.Parameters)
	bodySchema, hasBody := openAPIRequestSchema(operation)
	if !hasBody {
		if len(paramsSchema.Properties) == 0 {
			return IDLMessage{}, false
		}
		return openAPISchemaToMessage(name, paramsSchema, components), true
	}
	if bodySchema.Ref != "" && len(paramsSchema.Properties) == 0 {
		return IDLMessage{}, false
	}
	if bodySchema.Ref != "" {
		bodySchema = components[openAPIRefName(bodySchema.Ref)]
	}
	merged := openAPISpecSchema{Type: "object", Properties: map[string]openAPISpecSchema{}}
	for key, value := range paramsSchema.Properties {
		merged.Properties[key] = value
	}
	for key, value := range bodySchema.Properties {
		if _, exists := merged.Properties[key]; exists {
			merged.Properties["body"+exportName(key)] = value
			continue
		}
		merged.Properties[key] = value
	}
	if len(merged.Properties) == 0 {
		return IDLMessage{}, false
	}
	return openAPISchemaToMessage(name, merged, components), true
}

func openAPIParamsSchema(params []openAPIParameter) openAPISpecSchema {
	props := map[string]openAPISpecSchema{}
	for _, param := range params {
		if param.In != "path" && param.In != "query" {
			continue
		}
		props[param.Name] = param.Schema
	}
	return openAPISpecSchema{Type: "object", Properties: props}
}

func openAPIPathItemParameters(item openAPIPathItem, components map[string]openAPIParameter) []openAPIParameter {
	operation, ok := item[openAPIPathParametersKey]
	if !ok {
		return nil
	}
	return openAPIResolveParameters(operation.Parameters, components)
}

func openAPIResolveParameters(params []openAPIParameter, components map[string]openAPIParameter) []openAPIParameter {
	if len(params) == 0 {
		return nil
	}
	out := make([]openAPIParameter, 0, len(params))
	for _, param := range params {
		if param.Ref != "" {
			name := openAPIRefName(param.Ref)
			resolved, ok := components[name]
			if !ok {
				continue
			}
			param = resolved
		}
		out = append(out, param)
	}
	return out
}

func openAPIJoinParameters(base []openAPIParameter, override []openAPIParameter) []openAPIParameter {
	if len(base) == 0 {
		return override
	}
	if len(override) == 0 {
		return base
	}
	out := make([]openAPIParameter, 0, len(base)+len(override))
	seen := map[string]int{}
	for _, param := range base {
		key := openAPIParameterKey(param)
		seen[key] = len(out)
		out = append(out, param)
	}
	for _, param := range override {
		key := openAPIParameterKey(param)
		if idx, ok := seen[key]; ok {
			out[idx] = param
			continue
		}
		seen[key] = len(out)
		out = append(out, param)
	}
	return out
}

func openAPIParameterKey(param openAPIParameter) string {
	return strings.ToLower(param.In) + ":" + param.Name
}

func generateTypeScriptClient(doc IDLDocument, baseURL string) []byte {
	if baseURL == "" {
		baseURL = "''"
	} else {
		baseURL = fmt.Sprintf("%q", strings.TrimRight(baseURL, "/"))
	}
	var b bytes.Buffer
	fprintf(&b, "// Code generated by gofly api client. DO NOT EDIT.\n\n")
	for _, msg := range doc.Messages {
		fprintf(&b, "export interface %s {\n", exportName(msg.Name))
		for _, field := range msg.Fields {
			fprintf(&b, "  %s?: %s;\n", lowerCamel(field.Name), typeScriptType(field.Type))
		}
		fprintf(&b, "}\n\n")
	}
	fprintf(&b, "export class APIClient {\n")
	fprintf(&b, "  constructor(private readonly baseURL: string = %s) {}\n\n", baseURL)
	for _, svc := range doc.Services {
		for _, method := range svc.Methods {
			writeTypeScriptMethod(&b, method, messageByName(doc, method.Request))
		}
	}
	fprintf(&b, "}\n")
	return b.Bytes()
}

func writeTypeScriptMethod(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	methodName := lowerCamel(method.Name)
	requestType := exportName(method.Request)
	responseType := exportName(method.Response)
	if method.Request == "" {
		fprintf(b, "  async %s(): Promise<%s> {\n", methodName, responseType)
	} else {
		fprintf(b, "  async %s(req: %s): Promise<%s> {\n", methodName, requestType, responseType)
	}
	writeTypeScriptURL(b, method, request)
	fprintf(b, "    const init: RequestInit = { method: %q, headers: { 'Content-Type': 'application/json' } };\n", method.HTTPMethod)
	if method.Request != "" && method.HTTPMethod != "GET" {
		fprintf(b, "    init.body = JSON.stringify(req);\n")
	}
	fprintf(b, "    const resp = await fetch(url, init);\n")
	fprintf(b, "    if (!resp.ok) throw new Error(await resp.text());\n")
	fprintf(b, "    return await resp.json() as %s;\n", responseType)
	fprintf(b, "  }\n\n")
}

func writeTypeScriptURL(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	pathParams := openAPIPathParamNames(method.HTTPPath)
	if len(pathParams) == 0 && (strings.ToUpper(method.HTTPMethod) != "GET" || len(request.Fields) == 0) {
		fprintf(b, "    const url = this.baseURL + %q;\n", method.HTTPPath)
		return
	}
	fprintf(b, "    let path = %q;\n", method.HTTPPath)
	if len(request.Fields) > 0 {
		for _, name := range pathParams {
			field := clientFieldNameForParam(request, name)
			fprintf(b, "    path = path.replace(%q, encodeURIComponent(String(req.%s ?? '')));\n", "{"+name+"}", field)
		}
	}
	if strings.ToUpper(method.HTTPMethod) == "GET" && len(request.Fields) > 0 {
		writeTypeScriptQueryParams(b, request, pathParams)
		fprintf(b, "    const qs = query.toString();\n")
		fprintf(b, "    const url = this.baseURL + path + (qs ? '?' + qs : '');\n")
		return
	}
	fprintf(b, "    const url = this.baseURL + path;\n")
}

func writeTypeScriptQueryParams(b *bytes.Buffer, request IDLMessage, pathParams []string) {
	fprintf(b, "    const query = new URLSearchParams();\n")
	pathParamSet := clientParamSet(pathParams)
	for _, field := range request.Fields {
		name := lowerCamel(field.Name)
		if _, ok := pathParamSet[name]; ok {
			continue
		}
		writeTypeScriptQueryParam(b, name)
	}
}

func writeTypeScriptQueryParam(b *bytes.Buffer, name string) {
	fprintf(b, "    if (req.%s !== undefined && req.%s !== null) {\n", name, name)
	fprintf(b, "      if (Array.isArray(req.%s)) {\n", name)
	fprintf(b, "        for (const item of req.%s) query.append(%q, String(item));\n", name, name)
	fprintf(b, "      } else {\n")
	fprintf(b, "        query.append(%q, String(req.%s));\n", name, name)
	fprintf(b, "      }\n")
	fprintf(b, "    }\n")
}

func generateJavaScriptClient(doc IDLDocument, baseURL string) []byte {
	if baseURL == "" {
		baseURL = "''"
	} else {
		baseURL = fmt.Sprintf("%q", strings.TrimRight(baseURL, "/"))
	}
	var b bytes.Buffer
	fprintf(&b, "// Code generated by gofly api client. DO NOT EDIT.\n\n")
	fprintf(&b, "export class APIClient {\n")
	fprintf(&b, "  constructor(baseURL = %s) { this.baseURL = baseURL; }\n\n", baseURL)
	for _, svc := range doc.Services {
		for _, method := range svc.Methods {
			writeJavaScriptMethod(&b, method, messageByName(doc, method.Request))
		}
	}
	fprintf(&b, "}\n")
	return b.Bytes()
}

func writeJavaScriptMethod(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	methodName := lowerCamel(method.Name)
	if method.Request == "" {
		fprintf(b, "  async %s() {\n", methodName)
	} else {
		fprintf(b, "  async %s(req) {\n", methodName)
	}
	writeJavaScriptURL(b, method, request)
	fprintf(b, "    const init = { method: %q, headers: { 'Content-Type': 'application/json' } };\n", method.HTTPMethod)
	if method.Request != "" && method.HTTPMethod != "GET" {
		fprintf(b, "    init.body = JSON.stringify(req);\n")
	}
	fprintf(b, "    const resp = await fetch(url, init);\n")
	fprintf(b, "    if (!resp.ok) throw new Error(await resp.text());\n")
	fprintf(b, "    return await resp.json();\n")
	fprintf(b, "  }\n\n")
}

func writeJavaScriptURL(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	pathParams := openAPIPathParamNames(method.HTTPPath)
	if len(pathParams) == 0 && (strings.ToUpper(method.HTTPMethod) != "GET" || len(request.Fields) == 0) {
		fprintf(b, "    const url = this.baseURL + %q;\n", method.HTTPPath)
		return
	}
	fprintf(b, "    let path = %q;\n", method.HTTPPath)
	if len(request.Fields) > 0 {
		for _, name := range pathParams {
			field := clientFieldNameForParam(request, name)
			fprintf(b, "    path = path.replace(%q, encodeURIComponent(String(req.%s ?? '')));\n", "{"+name+"}", field)
		}
	}
	if strings.ToUpper(method.HTTPMethod) == "GET" && len(request.Fields) > 0 {
		writeJavaScriptQueryParams(b, request, pathParams)
		fprintf(b, "    const qs = query.toString();\n")
		fprintf(b, "    const url = this.baseURL + path + (qs ? '?' + qs : '');\n")
		return
	}
	fprintf(b, "    const url = this.baseURL + path;\n")
}

func writeJavaScriptQueryParams(b *bytes.Buffer, request IDLMessage, pathParams []string) {
	fprintf(b, "    const query = new URLSearchParams();\n")
	pathParamSet := clientParamSet(pathParams)
	for _, field := range request.Fields {
		name := lowerCamel(field.Name)
		if _, ok := pathParamSet[name]; ok {
			continue
		}
		writeJavaScriptQueryParam(b, name)
	}
}

func writeJavaScriptQueryParam(b *bytes.Buffer, name string) {
	fprintf(b, "    if (req.%s !== undefined && req.%s !== null) {\n", name, name)
	fprintf(b, "      if (Array.isArray(req.%s)) {\n", name)
	fprintf(b, "        for (const item of req.%s) query.append(%q, String(item));\n", name, name)
	fprintf(b, "      } else {\n")
	fprintf(b, "        query.append(%q, String(req.%s));\n", name, name)
	fprintf(b, "      }\n")
	fprintf(b, "    }\n")
}

func clientFieldNameForParam(request IDLMessage, param string) string {
	target := lowerCamel(param)
	for _, field := range request.Fields {
		name := lowerCamel(field.Name)
		if name == target || strings.EqualFold(field.Name, param) {
			return name
		}
	}
	return target
}

func clientParamSet(params []string) map[string]struct{} {
	out := make(map[string]struct{}, len(params))
	for _, param := range params {
		out[lowerCamel(param)] = struct{}{}
	}
	return out
}

func generateDartClient(doc IDLDocument, baseURL string) []byte {
	baseURL = strings.TrimRight(baseURL, "/")
	var b bytes.Buffer
	fprintf(&b, "// Code generated by gofly api dart. DO NOT EDIT.\n\n")
	fprintf(&b, "import 'dart:convert';\n")
	fprintf(&b, "import 'package:http/http.dart' as http;\n\n")
	for _, msg := range doc.Messages {
		writeDartModel(&b, msg)
	}
	fprintf(&b, "class APIClient {\n")
	fprintf(&b, "  APIClient({this.baseURL = %q, http.Client? client}) : _client = client ?? http.Client();\n\n", baseURL)
	fprintf(&b, "  final String baseURL;\n")
	fprintf(&b, "  final http.Client _client;\n\n")
	fprintf(&b, "  void close() => _client.close();\n\n")
	for _, svc := range doc.Services {
		for _, method := range svc.Methods {
			writeDartMethod(&b, method, messageByName(doc, method.Request))
		}
	}
	fprintf(&b, "}\n")
	return b.Bytes()
}

func writeDartModel(b *bytes.Buffer, msg IDLMessage) {
	typeName := exportName(msg.Name)
	fprintf(b, "class %s {\n", typeName)
	fprintf(b, "  %s({", typeName)
	for i, field := range msg.Fields {
		if i > 0 {
			fprintf(b, ", ")
		}
		fprintf(b, "this.%s", lowerCamel(field.Name))
	}
	fprintf(b, "});\n\n")
	for _, field := range msg.Fields {
		fprintf(b, "  final %s? %s;\n", dartType(field.Type), lowerCamel(field.Name))
	}
	fprintf(b, "\n  factory %s.fromJson(Map<String, dynamic> json) => %s(\n", typeName, typeName)
	for _, field := range msg.Fields {
		name := lowerCamel(field.Name)
		fprintf(b, "        %s: %s,\n", name, dartFromJSON(field.Type, "json[\""+name+"\"]"))
	}
	fprintf(b, "      );\n\n")
	fprintf(b, "  Map<String, dynamic> toJson() => {\n")
	for _, field := range msg.Fields {
		name := lowerCamel(field.Name)
		fprintf(b, "        %q: %s,\n", name, name)
	}
	fprintf(b, "      };\n")
	fprintf(b, "}\n\n")
}

func writeDartMethod(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	methodName := lowerCamel(method.Name)
	responseType := exportName(method.Response)
	if method.Request == "" {
		fprintf(b, "  Future<%s> %s() async {\n", responseType, methodName)
	} else {
		fprintf(b, "  Future<%s> %s(%s req) async {\n", responseType, methodName, exportName(method.Request))
	}
	writeDartURL(b, method, request)
	verb := strings.ToUpper(method.HTTPMethod)
	if method.Request != "" && verb != "GET" {
		fprintf(b, "    final resp = await _client.%s(uri, headers: {'Content-Type': 'application/json'}, body: jsonEncode(req.toJson()));\n", strings.ToLower(verb))
	} else {
		fprintf(b, "    final resp = await _client.%s(uri, headers: {'Content-Type': 'application/json'});\n", strings.ToLower(verb))
	}
	fprintf(b, "    if (resp.statusCode < 200 || resp.statusCode >= 300) {\n")
	fprintf(b, "      throw Exception(resp.body);\n")
	fprintf(b, "    }\n")
	fprintf(b, "    return %s.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);\n", responseType)
	fprintf(b, "  }\n\n")
}

func writeDartURL(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	pathParams := openAPIPathParamNames(method.HTTPPath)
	if len(pathParams) == 0 && (strings.ToUpper(method.HTTPMethod) != "GET" || len(request.Fields) == 0) {
		fprintf(b, "    final uri = Uri.parse('$baseURL%s');\n", method.HTTPPath)
		return
	}
	fprintf(b, "    var path = %q;\n", method.HTTPPath)
	if len(request.Fields) > 0 {
		for _, name := range pathParams {
			field := clientFieldNameForParam(request, name)
			fprintf(b, "    path = path.replaceAll(%q, Uri.encodeComponent((req.%s ?? '').toString()));\n", "{"+name+"}", field)
		}
	}
	if strings.ToUpper(method.HTTPMethod) == "GET" && len(request.Fields) > 0 {
		writeDartQueryParams(b, request, pathParams)
		fprintf(b, "    final qs = query.entries.expand((entry) => entry.value.map((value) => '${Uri.encodeQueryComponent(entry.key)}=${Uri.encodeQueryComponent(value)}')).join('&');\n")
		fprintf(b, "    final uri = Uri.parse('$baseURL$path${qs.isNotEmpty ? '?$qs' : ''}');\n")
		return
	}
	fprintf(b, "    final uri = Uri.parse('$baseURL$path');\n")
}

func writeDartQueryParams(b *bytes.Buffer, request IDLMessage, pathParams []string) {
	fprintf(b, "    final query = <String, List<String>>{};\n")
	fprintf(b, "    void addQuery(String name, Object? value) {\n")
	fprintf(b, "      if (value == null) return;\n")
	fprintf(b, "      if (value is Iterable) {\n")
	fprintf(b, "        for (final item in value) { addQuery(name, item); }\n")
	fprintf(b, "        return;\n")
	fprintf(b, "      }\n")
	fprintf(b, "      query.putIfAbsent(name, () => <String>[]).add(value.toString());\n")
	fprintf(b, "    }\n")
	pathParamSet := clientParamSet(pathParams)
	for _, field := range request.Fields {
		name := lowerCamel(field.Name)
		if _, ok := pathParamSet[name]; ok {
			continue
		}
		fprintf(b, "    addQuery(%q, req.%s);\n", name, name)
	}
}

func dartType(name string) string {
	if strings.HasPrefix(name, "[]") {
		return "List<" + dartType(strings.TrimPrefix(name, "[]")) + ">"
	}
	switch name {
	case "string":
		return "String"
	case "bool":
		return "bool"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "int"
	case "float32", "float64":
		return "double"
	default:
		return exportName(name)
	}
}

func dartFromJSON(name, expr string) string {
	if strings.HasPrefix(name, "[]") {
		elem := strings.TrimPrefix(name, "[]")
		return fmt.Sprintf("(%s as List<dynamic>?)?.map((e) => %s).toList()", expr, dartFromJSON(elem, "e"))
	}
	switch name {
	case "string":
		return expr + " as String?"
	case "bool":
		return expr + " as bool?"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "(" + expr + " as num?)?.toInt()"
	case "float32", "float64":
		return "(" + expr + " as num?)?.toDouble()"
	default:
		return exportName(name) + ".fromJson(" + expr + " as Map<String, dynamic>)"
	}
}

func generateJavaClient(doc IDLDocument, baseURL string) []byte {
	baseURL = strings.TrimRight(baseURL, "/")
	var b bytes.Buffer
	fprintf(&b, "// Code generated by gofly api java. DO NOT EDIT.\n\n")
	fprintf(&b, "import com.fasterxml.jackson.databind.ObjectMapper;\n")
	fprintf(&b, "import java.net.URI;\n")
	fprintf(&b, "import java.net.URLEncoder;\n")
	fprintf(&b, "import java.net.http.HttpClient;\n")
	fprintf(&b, "import java.net.http.HttpRequest;\n")
	fprintf(&b, "import java.net.http.HttpResponse;\n\n")
	fprintf(&b, "import java.nio.charset.StandardCharsets;\n\n")
	fprintf(&b, "public class APIClient {\n")
	fprintf(&b, "  private final String baseURL;\n")
	fprintf(&b, "  private final HttpClient client;\n")
	fprintf(&b, "  private final ObjectMapper mapper = new ObjectMapper();\n\n")
	fprintf(&b, "  public APIClient() { this(%q); }\n", baseURL)
	fprintf(&b, "  public APIClient(String baseURL) {\n")
	fprintf(&b, "    this.baseURL = baseURL == null ? \"\" : baseURL.replaceAll(\"/+$\", \"\");\n")
	fprintf(&b, "    this.client = HttpClient.newHttpClient();\n")
	fprintf(&b, "  }\n\n")
	for _, msg := range doc.Messages {
		writeJavaModel(&b, msg)
	}
	for _, svc := range doc.Services {
		for _, method := range svc.Methods {
			writeJavaMethod(&b, method, messageByName(doc, method.Request))
		}
	}
	writeJavaQueryHelper(&b)
	fprintf(&b, "}\n")
	return b.Bytes()
}

func writeJavaModel(b *bytes.Buffer, msg IDLMessage) {
	fprintf(b, "  public static class %s {\n", exportName(msg.Name))
	for _, field := range msg.Fields {
		fprintf(b, "    public %s %s;\n", javaType(field.Type), lowerCamel(field.Name))
	}
	fprintf(b, "  }\n\n")
}

func writeJavaMethod(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	methodName := lowerCamel(method.Name)
	responseType := exportName(method.Response)
	if method.Request == "" {
		fprintf(b, "  public %s %s() throws Exception {\n", responseType, methodName)
	} else {
		fprintf(b, "  public %s %s(%s req) throws Exception {\n", responseType, methodName, exportName(method.Request))
	}
	writeJavaURL(b, method, request)
	fprintf(b, "    HttpRequest.Builder builder = HttpRequest.newBuilder(URI.create(url));\n")
	verb := strings.ToUpper(method.HTTPMethod)
	if method.Request != "" && verb != "GET" {
		fprintf(b, "    builder.method(%q, HttpRequest.BodyPublishers.ofString(mapper.writeValueAsString(req)));\n", verb)
	} else {
		fprintf(b, "    builder.method(%q, HttpRequest.BodyPublishers.noBody());\n", verb)
	}
	fprintf(b, "    builder.header(\"Content-Type\", \"application/json\");\n")
	fprintf(b, "    HttpResponse<String> resp = client.send(builder.build(), HttpResponse.BodyHandlers.ofString());\n")
	fprintf(b, "    if (resp.statusCode() < 200 || resp.statusCode() >= 300) throw new RuntimeException(resp.body());\n")
	fprintf(b, "    return mapper.readValue(resp.body(), %s.class);\n", responseType)
	fprintf(b, "  }\n\n")
}

func writeJavaURL(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	pathParams := openAPIPathParamNames(method.HTTPPath)
	if len(pathParams) == 0 && (strings.ToUpper(method.HTTPMethod) != "GET" || len(request.Fields) == 0) {
		fprintf(b, "    String url = baseURL + %q;\n", method.HTTPPath)
		return
	}
	fprintf(b, "    String path = %q;\n", method.HTTPPath)
	if len(request.Fields) > 0 {
		for _, name := range pathParams {
			field := clientFieldNameForParam(request, name)
			fprintf(b, "    path = path.replace(%q, URLEncoder.encode(String.valueOf(req.%s == null ? \"\" : req.%s), StandardCharsets.UTF_8));\n", "{"+name+"}", field, field)
		}
	}
	if strings.ToUpper(method.HTTPMethod) == "GET" && len(request.Fields) > 0 {
		writeJavaQueryParams(b, request, pathParams)
		fprintf(b, "    String url = baseURL + path + (query.length() > 0 ? \"?\" + query : \"\");\n")
		return
	}
	fprintf(b, "    String url = baseURL + path;\n")
}

func writeJavaQueryParams(b *bytes.Buffer, request IDLMessage, pathParams []string) {
	fprintf(b, "    StringBuilder query = new StringBuilder();\n")
	pathParamSet := clientParamSet(pathParams)
	for _, field := range request.Fields {
		name := lowerCamel(field.Name)
		if _, ok := pathParamSet[name]; ok {
			continue
		}
		fprintf(b, "    appendQuery(query, %q, req.%s);\n", name, name)
	}
}

func writeJavaQueryHelper(b *bytes.Buffer) {
	fprintf(b, "  private static void appendQuery(StringBuilder query, String name, Object value) {\n")
	fprintf(b, "    if (value == null) return;\n")
	fprintf(b, "    if (value instanceof Iterable<?>) {\n")
	fprintf(b, "      for (Object item : (Iterable<?>) value) appendQuery(query, name, item);\n")
	fprintf(b, "      return;\n")
	fprintf(b, "    }\n")
	fprintf(b, "    if (query.length() > 0) query.append('&');\n")
	fprintf(b, "    query.append(URLEncoder.encode(name, StandardCharsets.UTF_8));\n")
	fprintf(b, "    query.append('=');\n")
	fprintf(b, "    query.append(URLEncoder.encode(String.valueOf(value), StandardCharsets.UTF_8));\n")
	fprintf(b, "  }\n")
}

func javaType(name string) string {
	if strings.HasPrefix(name, "[]") {
		return "java.util.List<" + javaBoxedType(strings.TrimPrefix(name, "[]")) + ">"
	}
	return javaBoxedType(name)
}

func javaBoxedType(name string) string {
	switch name {
	case "string":
		return "String"
	case "bool":
		return "Boolean"
	case "int", "int8", "int16", "int32", "uint8", "uint16", "uint32":
		return "Integer"
	case "int64", "uint", "uint64":
		return "Long"
	case "float32":
		return "Float"
	case "float64":
		return "Double"
	default:
		return exportName(name)
	}
}

func generateKotlinClient(doc IDLDocument, baseURL string) []byte {
	baseURL = strings.TrimRight(baseURL, "/")
	var b bytes.Buffer
	fprintf(&b, "// Code generated by gofly api kotlin. DO NOT EDIT.\n\n")
	fprintf(&b, "import kotlinx.serialization.Serializable\n")
	fprintf(&b, "import kotlinx.serialization.encodeToString\n")
	fprintf(&b, "import kotlinx.serialization.json.Json\n")
	fprintf(&b, "import java.net.URI\n")
	fprintf(&b, "import java.net.URLEncoder\n")
	fprintf(&b, "import java.net.http.HttpClient\n")
	fprintf(&b, "import java.net.http.HttpRequest\n")
	fprintf(&b, "import java.net.http.HttpResponse\n\n")
	fprintf(&b, "import java.nio.charset.StandardCharsets\n\n")
	for _, msg := range doc.Messages {
		writeKotlinModel(&b, msg)
	}
	fprintf(&b, "class APIClient(private val baseURL: String = %q) {\n", baseURL)
	fprintf(&b, "  private val client = HttpClient.newHttpClient()\n")
	fprintf(&b, "  private val json = Json { ignoreUnknownKeys = true }\n\n")
	for _, svc := range doc.Services {
		for _, method := range svc.Methods {
			writeKotlinMethod(&b, method, messageByName(doc, method.Request))
		}
	}
	writeKotlinQueryHelper(&b)
	fprintf(&b, "}\n")
	return b.Bytes()
}

func writeKotlinModel(b *bytes.Buffer, msg IDLMessage) {
	fprintf(b, "@Serializable\n")
	fprintf(b, "data class %s(\n", exportName(msg.Name))
	for i, field := range msg.Fields {
		comma := ","
		if i == len(msg.Fields)-1 {
			comma = ""
		}
		fprintf(b, "  val %s: %s? = null%s\n", lowerCamel(field.Name), kotlinType(field.Type), comma)
	}
	fprintf(b, ")\n\n")
}

func writeKotlinMethod(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	methodName := lowerCamel(method.Name)
	responseType := exportName(method.Response)
	if method.Request == "" {
		fprintf(b, "  fun %s(): %s {\n", methodName, responseType)
	} else {
		fprintf(b, "  fun %s(req: %s): %s {\n", methodName, exportName(method.Request), responseType)
	}
	writeKotlinURL(b, method, request)
	fprintf(b, "    val builder = HttpRequest.newBuilder(URI.create(url))\n")
	verb := strings.ToUpper(method.HTTPMethod)
	if method.Request != "" && verb != "GET" {
		fprintf(b, "      .method(%q, HttpRequest.BodyPublishers.ofString(json.encodeToString(req)))\n", verb)
	} else {
		fprintf(b, "      .method(%q, HttpRequest.BodyPublishers.noBody())\n", verb)
	}
	fprintf(b, "      .header(\"Content-Type\", \"application/json\")\n")
	fprintf(b, "    val resp = client.send(builder.build(), HttpResponse.BodyHandlers.ofString())\n")
	fprintf(b, "    if (resp.statusCode() !in 200..299) throw RuntimeException(resp.body())\n")
	fprintf(b, "    return json.decodeFromString(resp.body())\n")
	fprintf(b, "  }\n\n")
}

func writeKotlinURL(b *bytes.Buffer, method IDLMethod, request IDLMessage) {
	pathParams := openAPIPathParamNames(method.HTTPPath)
	if len(pathParams) == 0 && (strings.ToUpper(method.HTTPMethod) != "GET" || len(request.Fields) == 0) {
		fprintf(b, "    val url = baseURL.trimEnd('/') + %q\n", method.HTTPPath)
		return
	}
	fprintf(b, "    var path = %q\n", method.HTTPPath)
	if len(request.Fields) > 0 {
		for _, name := range pathParams {
			field := clientFieldNameForParam(request, name)
			fprintf(b, "    path = path.replace(%q, URLEncoder.encode((req.%s ?: \"\").toString(), StandardCharsets.UTF_8))\n", "{"+name+"}", field)
		}
	}
	if strings.ToUpper(method.HTTPMethod) == "GET" && len(request.Fields) > 0 {
		writeKotlinQueryParams(b, request, pathParams)
		fprintf(b, "    val url = baseURL.trimEnd('/') + path + if (query.isNotEmpty()) \"?$query\" else \"\"\n")
		return
	}
	fprintf(b, "    val url = baseURL.trimEnd('/') + path\n")
}

func writeKotlinQueryParams(b *bytes.Buffer, request IDLMessage, pathParams []string) {
	fprintf(b, "    val query = StringBuilder()\n")
	pathParamSet := clientParamSet(pathParams)
	for _, field := range request.Fields {
		name := lowerCamel(field.Name)
		if _, ok := pathParamSet[name]; ok {
			continue
		}
		fprintf(b, "    appendQuery(query, %q, req.%s)\n", name, name)
	}
}

func writeKotlinQueryHelper(b *bytes.Buffer) {
	fprintf(b, "  private fun appendQuery(query: StringBuilder, name: String, value: Any?) {\n")
	fprintf(b, "    if (value == null) return\n")
	fprintf(b, "    if (value is Iterable<*>) {\n")
	fprintf(b, "      value.forEach { appendQuery(query, name, it) }\n")
	fprintf(b, "      return\n")
	fprintf(b, "    }\n")
	fprintf(b, "    if (query.isNotEmpty()) query.append('&')\n")
	fprintf(b, "    query.append(URLEncoder.encode(name, StandardCharsets.UTF_8))\n")
	fprintf(b, "    query.append('=')\n")
	fprintf(b, "    query.append(URLEncoder.encode(value.toString(), StandardCharsets.UTF_8))\n")
	fprintf(b, "  }\n")
}

func kotlinType(name string) string {
	if strings.HasPrefix(name, "[]") {
		return "List<" + kotlinType(strings.TrimPrefix(name, "[]")) + ">"
	}
	switch name {
	case "string":
		return "String"
	case "bool":
		return "Boolean"
	case "int", "int8", "int16", "int32", "uint8", "uint16", "uint32":
		return "Int"
	case "int64", "uint", "uint64":
		return "Long"
	case "float32":
		return "Float"
	case "float64":
		return "Double"
	default:
		return exportName(name)
	}
}

func typeScriptType(name string) string {
	if strings.HasPrefix(name, "[]") {
		return typeScriptType(strings.TrimPrefix(name, "[]")) + "[]"
	}
	switch name {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
		return "number"
	default:
		return exportName(name)
	}
}

func generateAPIMarkdown(doc IDLDocument) []byte {
	var b bytes.Buffer
	title := "API"
	if len(doc.Services) > 0 {
		title = exportName(doc.Services[0].Name) + " API"
	}
	fprintf(&b, "# %s\n\n", title)
	if len(doc.Services) > 0 {
		fprintf(&b, "## Routes\n\n")
		fprintf(&b, "| Method | Path | Handler | Request | Response |\n")
		fprintf(&b, "| --- | --- | --- | --- | --- |\n")
		for _, svc := range doc.Services {
			for _, method := range svc.Methods {
				fprintf(
					&b,
					"| %s | `%s` | `%s` | `%s` | `%s` |\n",
					method.HTTPMethod,
					method.HTTPPath,
					exportName(method.Name),
					exportName(method.Request),
					exportName(method.Response),
				)
			}
		}
		b.WriteByte('\n')
	}
	if len(doc.Messages) > 0 {
		fprintf(&b, "## Types\n\n")
		for _, msg := range doc.Messages {
			fprintf(&b, "### %s\n\n", exportName(msg.Name))
			fprintf(&b, "| Field | Type |\n| --- | --- |\n")
			for _, field := range msg.Fields {
				fprintf(&b, "| %s | `%s` |\n", exportName(field.Name), field.Type)
			}
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

func generateAPIOpenAPI(doc IDLDocument) ([]byte, error) {
	spec := buildAPIOpenAPISpec(doc)
	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal openapi doc: %w", err)
	}
	return append(out, '\n'), nil
}

func generateAPIOpenAPIYAML(doc IDLDocument) ([]byte, error) {
	spec := buildAPIOpenAPISpec(doc)
	out, err := yaml.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal openapi yaml doc: %w", err)
	}
	return out, nil
}

func buildAPIOpenAPISpec(doc IDLDocument) map[string]any {
	title := "API"
	if len(doc.Services) > 0 {
		title = exportName(doc.Services[0].Name) + " API"
	}
	tags := openAPITags(doc)
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   title,
			"version": "1.0.0",
		},
		"paths": map[string]any{},
		"components": map[string]any{
			"schemas":         map[string]any{},
			"securitySchemes": openAPISecuritySchemes(doc),
		},
	}
	if len(tags) > 0 {
		spec["tags"] = tags
	}
	paths := spec["paths"].(map[string]any)
	messageNames := openAPIMessageNames(doc)
	for _, svc := range doc.Services {
		tag := openAPIServiceTag(svc)
		for _, method := range svc.Methods {
			path := openAPIServicePath(svc, method.HTTPPath)
			pathItem, _ := paths[path].(map[string]any)
			if pathItem == nil {
				pathItem = map[string]any{}
				paths[path] = pathItem
			}
			operation := map[string]any{
				"operationId": exportName(method.Name),
				"tags":        []string{tag},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OK",
						"content":     jsonContentRef(method.Response, messageByName(doc, method.Response), messageNames),
					},
				},
			}
			if security := openAPIOperationSecurity(svc); len(security) > 0 {
				operation["security"] = security
			}
			if parameters := openAPIPathParameters(path, messageByName(doc, method.Request), messageNames); len(parameters) > 0 {
				operation["parameters"] = parameters
			}
			if method.Request != "" && method.HTTPMethod == "GET" {
				operation["parameters"] = appendOpenAPIQueryParameters(
					operation["parameters"],
					messageByName(doc, method.Request),
					messageNames,
					openAPIPathParamNames(path),
				)
			}
			if method.Request != "" && method.HTTPMethod != "GET" {
				operation["requestBody"] = map[string]any{
					"required": true,
					"content":  jsonContentRef(method.Request, messageByName(doc, method.Request), messageNames),
				}
			}
			pathItem[strings.ToLower(method.HTTPMethod)] = operation
		}
	}
	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	for _, msg := range doc.Messages {
		schemas[exportName(msg.Name)] = openAPIMessageSchema(msg, messageNames)
	}
	return spec
}

func openAPITags(doc IDLDocument) []map[string]any {
	seen := map[string]struct{}{}
	out := make([]map[string]any, 0, len(doc.Services))
	for _, svc := range doc.Services {
		tag := openAPIServiceTag(svc)
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		item := map[string]any{"name": tag}
		if svc.Server.Group != "" {
			item["description"] = "@server group " + svc.Server.Group
		}
		out = append(out, item)
	}
	return out
}

func openAPIServiceTag(svc IDLService) string {
	if svc.Server.Group != "" {
		return exportName(svc.Server.Group)
	}
	return exportName(svc.Name)
}

func openAPISecuritySchemes(doc IDLDocument) map[string]any {
	for _, svc := range doc.Services {
		if svc.Server.JWT != "" {
			return map[string]any{
				"BearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
				},
			}
		}
	}
	return map[string]any{}
}

func openAPIOperationSecurity(svc IDLService) []map[string][]string {
	if svc.Server.JWT == "" {
		return nil
	}
	return []map[string][]string{{"BearerAuth": []string{}}}
}

func openAPIServicePath(svc IDLService, path string) string {
	prefix := strings.TrimRight(strings.TrimSpace(svc.Server.Prefix), "/")
	if prefix == "" || prefix == "/" || strings.HasPrefix(path, prefix+"/") || path == prefix {
		return path
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return prefix + path
}

func openAPIMessageNames(doc IDLDocument) map[string]struct{} {
	out := make(map[string]struct{}, len(doc.Messages))
	for _, msg := range doc.Messages {
		out[exportName(msg.Name)] = struct{}{}
	}
	return out
}

func jsonContentRef(name string, msg IDLMessage, messageNames map[string]struct{}) map[string]any {
	if name == "" {
		return map[string]any{}
	}
	media := map[string]any{
		"schema": map[string]any{"$ref": "#/components/schemas/" + exportName(name)},
	}
	if example := openAPIMessageExample(msg, messageNames); len(example) > 0 {
		media["example"] = example
	}
	return map[string]any{
		"application/json": media,
	}
}

func messageByName(doc IDLDocument, name string) IDLMessage {
	for _, msg := range doc.Messages {
		if exportName(msg.Name) == exportName(name) {
			return msg
		}
	}
	return IDLMessage{}
}

func openAPIPathParameters(path string, msg IDLMessage, messageNames map[string]struct{}) []map[string]any {
	names := openAPIPathParamNames(path)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		field, ok := openAPIMessageField(msg, name)
		schema := map[string]any{"type": "string"}
		if ok {
			schema = openAPISchema(field.Type, messageNames)
		}
		out = append(out, map[string]any{
			"name":     name,
			"in":       "path",
			"required": true,
			"schema":   schema,
		})
	}
	return out
}

func openAPIPathParamNames(path string) []string {
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
		name := normalizeAPIPathParamName(strings.TrimSpace(path[:end]))
		if name != "" {
			names = append(names, name)
		}
		path = path[end+1:]
	}
}

func normalizeAPIPathParamName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "...")
	if name == "path" {
		return ""
	}
	return name
}

func appendOpenAPIQueryParameters(existing any, msg IDLMessage, messageNames map[string]struct{}, pathParamNames []string) []map[string]any {
	var out []map[string]any
	if values, ok := existing.([]map[string]any); ok {
		out = append(out, values...)
	}
	pathParams := make(map[string]struct{}, len(pathParamNames))
	for _, name := range pathParamNames {
		name = lowerCamel(name)
		if name != "" {
			pathParams[name] = struct{}{}
		}
	}
	for _, field := range msg.Fields {
		fieldName := openAPIFieldName(field)
		if _, ok := pathParams[fieldName]; ok {
			continue
		}
		param := map[string]any{
			"name":     fieldName,
			"in":       "query",
			"required": false,
			"schema":   openAPISchema(field.Type, messageNames),
		}
		if example, ok := openAPIFieldExample(field, messageNames); ok {
			param["example"] = example
		}
		out = append(out, param)
	}
	return out
}

func openAPIMessageSchema(msg IDLMessage, messageNames map[string]struct{}) map[string]any {
	props := map[string]any{}
	required := make([]string, 0, len(msg.Fields))
	for _, field := range msg.Fields {
		fieldName := openAPIFieldName(field)
		if fieldName == "-" || fieldName == "" {
			continue
		}
		schema := openAPISchema(field.Type, messageNames)
		if example, ok := openAPIFieldExample(field, messageNames); ok {
			schema["example"] = example
		}
		props[fieldName] = schema
		if openAPIFieldRequired(field) {
			required = append(required, fieldName)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func openAPIMessageExample(msg IDLMessage, messageNames map[string]struct{}) map[string]any {
	if msg.Name == "" || len(msg.Fields) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, field := range msg.Fields {
		fieldName := openAPIFieldName(field)
		if fieldName == "" || fieldName == "-" {
			continue
		}
		if example, ok := openAPIFieldExample(field, messageNames); ok {
			out[fieldName] = example
			continue
		}
		out[fieldName] = openAPIDefaultExample(field.Type, messageNames)
	}
	return out
}

func openAPIMessageField(msg IDLMessage, name string) (IDLField, bool) {
	for _, field := range msg.Fields {
		if openAPIFieldName(field) == lowerCamel(name) || strings.EqualFold(field.Name, name) {
			return field, true
		}
	}
	return IDLField{}, false
}

func openAPIFieldName(field IDLField) string {
	if field.Tag != "" {
		if name := strings.Split(reflect.StructTag(field.Tag).Get("json"), ",")[0]; name != "" {
			return name
		}
		if name := strings.Split(reflect.StructTag(field.Tag).Get("path"), ",")[0]; name != "" {
			return name
		}
		if name := strings.Split(reflect.StructTag(field.Tag).Get("form"), ",")[0]; name != "" {
			return name
		}
	}
	return lowerCamel(field.Name)
}

func openAPIFieldRequired(field IDLField) bool {
	if field.Tag == "" {
		return true
	}
	jsonTag := reflect.StructTag(field.Tag).Get("json")
	if strings.Contains(jsonTag, "omitempty") || strings.Contains(field.Tag, `optional:"true"`) {
		return false
	}
	return true
}

func openAPIFieldExample(field IDLField, messageNames map[string]struct{}) (any, bool) {
	if field.Tag == "" {
		return nil, false
	}
	value := reflect.StructTag(field.Tag).Get("example")
	if value == "" {
		return nil, false
	}
	return openAPIExampleValue(field.Type, value, messageNames), true
}

func openAPIExampleValue(fieldType string, value string, messageNames map[string]struct{}) any {
	schemaType := openAPIType(fieldType)
	if strings.HasPrefix(fieldType, "[]") {
		return []any{openAPIExampleValue(strings.TrimPrefix(fieldType, "[]"), value, messageNames)}
	}
	if _, ok := messageNames[exportName(fieldType)]; ok {
		return map[string]any{}
	}
	switch schemaType {
	case "integer":
		return 1
	case "number":
		return 1.23
	case "boolean":
		return value == "true"
	default:
		return value
	}
}

func openAPIDefaultExample(fieldType string, messageNames map[string]struct{}) any {
	if strings.HasPrefix(fieldType, "[]") {
		return []any{openAPIDefaultExample(strings.TrimPrefix(fieldType, "[]"), messageNames)}
	}
	if _, ok := messageNames[exportName(fieldType)]; ok {
		return map[string]any{}
	}
	switch openAPIType(fieldType) {
	case "integer":
		return 1
	case "number":
		return 1.23
	case "boolean":
		return true
	default:
		return "string"
	}
}

func openAPISchema(name string, messageNames map[string]struct{}) map[string]any {
	if strings.HasPrefix(name, "[]") {
		return map[string]any{"type": "array", "items": openAPISchema(strings.TrimPrefix(name, "[]"), messageNames)}
	}
	if _, ok := messageNames[exportName(name)]; ok {
		return map[string]any{"$ref": "#/components/schemas/" + exportName(name)}
	}
	schema := map[string]any{"type": openAPIType(name)}
	switch name {
	case "int32", "uint32":
		schema["format"] = "int32"
	case "int", "int64", "uint", "uint64", "uint8", "uint16", "int8", "int16":
		schema["format"] = "int64"
	case "float32":
		schema["format"] = "float"
	case "float64":
		schema["format"] = "double"
	}
	return schema
}

func openAPIType(name string) string {
	switch strings.TrimPrefix(name, "[]") {
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "integer"
	case "float32", "float64":
		return "number"
	default:
		return "string"
	}
}

func writeRESTFiles(doc IDLDocument, opts APIOptions) error {
	if len(doc.Services) == 0 {
		return errors.New("api service is required")
	}
	rpcAlias := rpcPackageAlias(opts.RPCPackage)
	opts.Dir = filepath.Join(opts.Dir, "v1")
	if err := ensureGeneratedDir(opts.Dir); err != nil {
		return fmt.Errorf("create api output directory: %w", err)
	}

	// types file: shared message definitions
	if opts.TypeGroup {
		if err := writeGroupedTypesFiles(opts.Dir, doc, opts.Package); err != nil {
			return err
		}
	} else {
		if err := writeTypesFile(opts.Dir, doc, opts.Package); err != nil {
			return err
		}
	}

	// one file per service containing the interface, gateway (when applicable), and route registration
	for _, svc := range doc.Services {
		svcDir := filepath.Join(opts.Dir, lowerSnake(svc.Name))
		if err := ensureGeneratedDir(svcDir); err != nil {
			return fmt.Errorf("create api service directory: %w", err)
		}
		if err := writeTypesFile(svcDir, doc, opts.Package); err != nil {
			return err
		}
		if err := writeServiceInterfaceFile(svcDir, svc, opts.Package, rpcAlias, opts.RPCPackage); err != nil {
			return err
		}
		if opts.RPCPackage != "" {
			if err := writeGatewayFile(svcDir, svc, opts.Package, rpcAlias, opts.RPCPackage); err != nil {
				return err
			}
		}
		// one file per method
		for _, method := range svc.Methods {
			if err := writeMethodFile(svcDir, method, svc, opts.Package, rpcAlias, opts.RPCPackage); err != nil {
				return err
			}
			if opts.RPCPackage != "" {
				if err := writeGatewayMethodFile(svcDir, method, svc, opts.Package, rpcAlias, opts.RPCPackage); err != nil {
					return err
				}
			}
		}
		// route registration file
		if err := writeRoutesFile(svcDir, svc, opts.Package, rpcAlias, opts.RPCPackage); err != nil {
			return err
		}
		if opts.Test {
			if err := writeAPITestFile(svcDir, svc, opts.Package); err != nil {
				return err
			}
		}
	}

	if opts.RPCPackage != "" {
		if err := writeConvertersFile(opts.Dir, doc, opts.Package, rpcAlias, opts.RPCPackage); err != nil {
			return err
		}
	}
	return nil
}

func writeTypesFile(dir string, doc IDLDocument, pkg string) error {
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", pkg)
	for _, msg := range doc.Messages {
		writeAPIMessage(&b, msg)
	}
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format types file: %w", err)
	}
	return writeGeneratedFile(filepath.Join(dir, "types.go"), formatted)
}

func writeGroupedTypesFiles(dir string, doc IDLDocument, pkg string) error {
	if len(doc.Messages) == 0 {
		return writeTypesFile(dir, doc, pkg)
	}
	for _, msg := range doc.Messages {
		var b bytes.Buffer
		fprintf(&b, "package %s\n\n", pkg)
		writeAPIMessage(&b, msg)
		formatted, err := format.Source(b.Bytes())
		if err != nil {
			return fmt.Errorf("format grouped type %s: %w", msg.Name, err)
		}
		path := filepath.Join(dir, "types_"+lowerSnake(msg.Name)+".go")
		if err := writeGeneratedFile(path, formatted); err != nil {
			return fmt.Errorf("write grouped api type %s: %w", msg.Name, err)
		}
	}
	return nil
}

func writeServiceInterfaceFile(dir string, svc IDLService, pkg, rpcAlias, rpcPkg string) error {
	var b bytes.Buffer
	serviceName := exportName(svc.Name)
	fprintf(&b, "package %s\n\n", pkg)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, ")\n\n")
	fprintf(&b, "type %s interface {\n", serviceName)
	for _, method := range svc.Methods {
		requestName := exportName(method.Request)
		if method.Request == "" {
			fprintf(&b, "\t%s(ctx context.Context) (*%s, error)\n", exportName(method.Name), exportName(method.Response))
			continue
		}
		fprintf(&b, "\t%s(ctx context.Context, req *%s) (*%s, error)\n", exportName(method.Name), requestName, exportName(method.Response))
	}
	fprintf(&b, "}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format service interface file: %w", err)
	}
	return writeGeneratedFile(filepath.Join(dir, "service.go"), formatted)
}

func writeGatewayFile(dir string, svc IDLService, pkg, rpcAlias, rpcPkg string) error {
	var b bytes.Buffer
	serviceName := exportName(svc.Name)
	gatewayName := serviceName + "Gateway"
	fprintf(&b, "package %s\n\n", pkg)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"github.com/imajinyun/gofly/rpc\"\n")
	fprintf(&b, "\t%s %q\n", rpcAlias, rpcPkg)
	fprintf(&b, ")\n\n")
	fprintf(&b, "type %s struct {\n\tclient *%s.%sClient\n}\n\n", gatewayName, rpcAlias, serviceName)
	fprintf(&b, "func New%s(cc rpc.Client) *%s {\n", gatewayName, gatewayName)
	fprintf(&b, "\treturn &%s{client: %s.New%sClient(cc)}\n", gatewayName, rpcAlias, serviceName)
	fprintf(&b, "}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format gateway file: %w", err)
	}
	return writeGeneratedFile(filepath.Join(dir, "gateway.go"), formatted)
}

func writeMethodFile(dir string, method IDLMethod, svc IDLService, pkg, rpcAlias, rpcPkg string) error {
	var b bytes.Buffer
	serviceName := exportName(svc.Name)
	methodName := exportName(method.Name)
	requestName := exportName(method.Request)
	fprintf(&b, "package %s\n\n", pkg)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"net/http\"\n\n")
	fprintf(&b, "\t\"github.com/imajinyun/gofly/rest\"\n")
	fprintf(&b, ")\n\n")
	fprintf(&b, "func Register%sRoute(s *rest.Server, impl %s) {\n", methodName, serviceName)
	fprintf(&b, "\ts.AddRoute(rest.Route{Method: http.Method%s, Path: %q, Handler: func(ctx *rest.Context) {\n", exportName(strings.ToLower(method.HTTPMethod)), method.HTTPPath)
	if method.Request != "" {
		fprintf(&b, "\t\tvar req %s\n", requestName)
		fprintf(&b, "\t\tif err := ctx.BindRequest(&req); err != nil {\n")
		fprintf(&b, "\t\t\tctx.Error(err)\n")
		fprintf(&b, "\t\t\treturn\n")
		fprintf(&b, "\t\t}\n")
		fprintf(&b, "\t\tresp, err := impl.%s(ctx.Request.Context(), &req)\n", methodName)
	} else {
		fprintf(&b, "\t\tresp, err := impl.%s(ctx.Request.Context())\n", methodName)
	}
	fprintf(&b, "\t\tif err != nil {\n")
	fprintf(&b, "\t\t\tctx.JSON(http.StatusInternalServerError, map[string]string{\"error\": err.Error()})\n")
	fprintf(&b, "\t\t\treturn\n")
	fprintf(&b, "\t\t}\n")
	fprintf(&b, "\t\tctx.JSON(http.StatusOK, resp)\n")
	fprintf(&b, "\t}})\n")
	fprintf(&b, "}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format method file: %w", err)
	}
	filename := lowerSnake(method.Name) + ".go"
	return writeGeneratedFile(filepath.Join(dir, filename), formatted)
}

func writeGatewayMethodFile(dir string, method IDLMethod, svc IDLService, pkg, rpcAlias, rpcPkg string) error {
	var b bytes.Buffer
	serviceName := exportName(svc.Name)
	gatewayName := serviceName + "Gateway"
	methodName := exportName(method.Name)
	requestName := exportName(method.Request)
	responseName := exportName(method.Response)
	fprintf(&b, "package %s\n\n", pkg)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t%s %q\n", rpcAlias, rpcPkg)
	fprintf(&b, ")\n\n")
	if method.Request == "" {
		fprintf(&b, "func (g *%s) %s(ctx context.Context) (*%s, error) {\n", gatewayName, methodName, responseName)
		fprintf(&b, "\tresp, err := g.client.%s(ctx, &%s.Empty{})\n", methodName, rpcAlias)
	} else {
		fprintf(&b, "func (g *%s) %s(ctx context.Context, req *%s) (*%s, error) {\n", gatewayName, methodName, requestName, responseName)
		fprintf(&b, "\tresp, err := g.client.%s(ctx, toRPC%s(req))\n", methodName, requestName)
	}
	fprintf(&b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(&b, "\treturn fromRPC%s(resp), nil\n", responseName)
	fprintf(&b, "}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format gateway method file: %w", err)
	}
	filename := lowerSnake(method.Name) + "_gateway.go"
	return writeGeneratedFile(filepath.Join(dir, filename), formatted)
}

func writeRoutesFile(dir string, svc IDLService, pkg, rpcAlias, rpcPkg string) error {
	var b bytes.Buffer
	serviceName := exportName(svc.Name)
	fprintf(&b, "package %s\n\n", pkg)
	fprintf(&b, "import (\n")
	if rpcPkg != "" {
		fprintf(&b, "\t\"github.com/imajinyun/gofly/rpc\"\n")
	}
	fprintf(&b, "\t\"github.com/imajinyun/gofly/rest\"\n")
	fprintf(&b, ")\n\n")
	fprintf(&b, "func Register%sRoutes(s *rest.Server, impl %s) {\n", serviceName, serviceName)
	for _, method := range svc.Methods {
		fprintf(&b, "\tRegister%sRoute(s, impl)\n", exportName(method.Name))
	}
	fprintf(&b, "}\n")
	if rpcPkg != "" {
		fprintf(&b, "\nfunc Register%sGatewayRoutes(s *rest.Server, cc rpc.Client) {\n", serviceName)
		fprintf(&b, "\tRegister%sRoutes(s, New%sGateway(cc))\n", serviceName, serviceName)
		fprintf(&b, "}\n")
	}
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format routes file: %w", err)
	}
	return writeGeneratedFile(filepath.Join(dir, "routes.go"), formatted)
}

func writeAPITestFile(dir string, svc IDLService, pkg string) error {
	var b bytes.Buffer
	serviceName := exportName(svc.Name)
	fprintf(&b, "package %s\n\n", pkg)
	fprintf(&b, "import \"testing\"\n\n")
	fprintf(&b, "func Test%sRoutesGenerated(t *testing.T) {\n", serviceName)
	fprintf(&b, "\tt.Helper()\n")
	fprintf(&b, "\t// This scaffold pins generated route test placement; add handler assertions here.\n")
	fprintf(&b, "}\n")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format api test file: %w", err)
	}
	return writeGeneratedFile(filepath.Join(dir, "routes_test.go"), formatted)
}

func writeConvertersFile(dir string, doc IDLDocument, pkg, rpcAlias, rpcPkg string) error {
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", pkg)
	for _, msg := range doc.Messages {
		writeRESTGatewayConverters(&b, msg, rpcAlias)
	}
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format converters file: %w", err)
	}
	return writeGeneratedFile(filepath.Join(dir, "converters.go"), formatted)
}

// legacy: kept for tests; generates a single-file blob
func GenerateRESTCode(doc IDLDocument, packageName string) ([]byte, error) {
	return GenerateRESTCodeWithOptions(doc, RESTCodeOptions{Package: packageName})
}

func GenerateRESTCodeWithOptions(doc IDLDocument, opts RESTCodeOptions) ([]byte, error) {
	if len(doc.Services) == 0 {
		return nil, errors.New("api service is required")
	}
	if opts.Package == "" {
		opts.Package = "api"
	}
	rpcAlias := rpcPackageAlias(opts.RPCPackage)
	var b bytes.Buffer
	fprintf(&b, "package %s\n\n", opts.Package)
	fprintf(&b, "import (\n")
	fprintf(&b, "\t\"context\"\n")
	fprintf(&b, "\t\"net/http\"\n")
	if opts.RPCPackage != "" {
		fprintf(&b, "\t\"github.com/imajinyun/gofly/rpc\"\n")
		fprintf(&b, "\t%s %q\n", rpcAlias, opts.RPCPackage)
	}
	fprintf(&b, "\t\"github.com/imajinyun/gofly/rest\"\n")
	fprintf(&b, ")\n\n")
	for _, msg := range doc.Messages {
		writeAPIMessage(&b, msg)
	}
	for _, svc := range doc.Services {
		writeRESTService(&b, svc)
		if opts.RPCPackage != "" {
			writeRESTGateway(&b, svc, rpcAlias)
		}
	}
	if opts.RPCPackage != "" {
		for _, msg := range doc.Messages {
			writeRESTGatewayConverters(&b, msg, rpcAlias)
		}
	}
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated api code: %w", err)
	}
	return out, nil
}

func writeRESTGateway(b *bytes.Buffer, svc IDLService, rpcAlias string) {
	serviceName := exportName(svc.Name)
	gatewayName := serviceName + "Gateway"
	fprintf(b, "type %s struct {\n\tclient *%s.%sClient\n}\n\n", gatewayName, rpcAlias, serviceName)
	fprintf(b, "func New%s(cc rpc.Client) *%s {\n", gatewayName, gatewayName)
	fprintf(b, "\treturn &%s{client: %s.New%sClient(cc)}\n", gatewayName, rpcAlias, serviceName)
	fprintf(b, "}\n\n")
	for _, method := range svc.Methods {
		writeRESTGatewayMethod(b, method, gatewayName, rpcAlias)
	}
	fprintf(b, "func Register%sGatewayRoutes(s *rest.Server, cc rpc.Client) {\n", serviceName)
	fprintf(b, "\tRegister%sRoutes(s, New%s(cc))\n", serviceName, gatewayName)
	fprintf(b, "}\n\n")
}

func writeRESTGatewayMethod(b *bytes.Buffer, method IDLMethod, gatewayName string, rpcAlias string) {
	methodName := exportName(method.Name)
	requestName := exportName(method.Request)
	responseName := exportName(method.Response)
	if method.Request == "" {
		fprintf(b, "func (g *%s) %s(ctx context.Context) (*%s, error) {\n", gatewayName, methodName, responseName)
		fprintf(b, "\tresp, err := g.client.%s(ctx, &%s.Empty{})\n", methodName, rpcAlias)
	} else {
		fprintf(b, "func (g *%s) %s(ctx context.Context, req *%s) (*%s, error) {\n", gatewayName, methodName, requestName, responseName)
		fprintf(b, "\tresp, err := g.client.%s(ctx, toRPC%s(req))\n", methodName, requestName)
	}
	fprintf(b, "\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fprintf(b, "\treturn fromRPC%s(resp), nil\n", responseName)
	fprintf(b, "}\n\n")
}

func writeRESTGatewayConverters(b *bytes.Buffer, msg IDLMessage, rpcAlias string) {
	messageName := exportName(msg.Name)
	fprintf(b, "func toRPC%s(in *%s) *%s.%s {\n", messageName, messageName, rpcAlias, messageName)
	fprintf(b, "\tif in == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\treturn &%s.%s{\n", rpcAlias, messageName)
	for _, field := range msg.Fields {
		fieldName := exportName(field.Name)
		fprintf(b, "\t\t%s: in.%s,\n", fieldName, fieldName)
	}
	fprintf(b, "\t}\n")
	fprintf(b, "}\n\n")
	fprintf(b, "func fromRPC%s(in *%s.%s) *%s {\n", messageName, rpcAlias, messageName, messageName)
	fprintf(b, "\tif in == nil {\n\t\treturn nil\n\t}\n")
	fprintf(b, "\treturn &%s{\n", messageName)
	for _, field := range msg.Fields {
		fieldName := exportName(field.Name)
		fprintf(b, "\t\t%s: in.%s,\n", fieldName, fieldName)
	}
	fprintf(b, "\t}\n")
	fprintf(b, "}\n\n")
}

func writeAPIMessage(b *bytes.Buffer, msg IDLMessage) {
	fprintf(b, "type %s struct {\n", exportName(msg.Name))
	for _, field := range msg.Fields {
		fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", exportName(field.Name), apiGoType(field.Type), lowerCamel(field.Name))
	}
	fprintf(b, "}\n\n")
}

func writeRESTService(b *bytes.Buffer, svc IDLService) {
	serviceName := exportName(svc.Name)
	fprintf(b, "type %s interface {\n", serviceName)
	for _, method := range svc.Methods {
		requestName := exportName(method.Request)
		if method.Request == "" {
			fprintf(b, "\t%s(ctx context.Context) (*%s, error)\n", exportName(method.Name), exportName(method.Response))
			continue
		}
		fprintf(b, "\t%s(ctx context.Context, req *%s) (*%s, error)\n", exportName(method.Name), requestName, exportName(method.Response))
	}
	fprintf(b, "}\n\n")
	fprintf(b, "func Register%sRoutes(s *rest.Server, impl %s) {\n", serviceName, serviceName)
	for _, method := range svc.Methods {
		writeRESTRoute(b, method)
	}
	fprintf(b, "}\n\n")
}

func writeRESTRoute(b *bytes.Buffer, method IDLMethod) {
	methodName := exportName(method.Name)
	fprintf(b, "\ts.AddRoute(rest.Route{Method: http.Method%s, Path: %q, Handler: func(ctx *rest.Context) {\n", exportName(strings.ToLower(method.HTTPMethod)), method.HTTPPath)
	if method.Request != "" {
		requestName := exportName(method.Request)
		fprintf(b, "\t\tvar req %s\n", requestName)
		fprintf(b, "\t\tif err := ctx.BindRequest(&req); err != nil {\n\t\t\tctx.Error(err)\n\t\t\treturn\n\t\t}\n")
		fprintf(b, "\t\tresp, err := impl.%s(ctx.Request.Context(), &req)\n", methodName)
	} else {
		fprintf(b, "\t\tresp, err := impl.%s(ctx.Request.Context())\n", methodName)
	}
	fprintf(b, "\t\tif err != nil {\n\t\t\tctx.JSON(http.StatusInternalServerError, map[string]string{\"error\": err.Error()})\n\t\t\treturn\n\t\t}\n")
	fprintf(b, "\t\tctx.JSON(http.StatusOK, resp)\n")
	fprintf(b, "\t}})\n")
}

func apiGoType(apiType string) string {
	switch apiType {
	case "string":
		return "string"
	case "bool":
		return "bool"
	case "int", "int8", "int16", "int32", "int64":
		return apiType
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return apiType
	case "float32", "float64":
		return apiType
	default:
		if strings.HasPrefix(apiType, "[]") {
			return "[]" + apiGoType(strings.TrimPrefix(apiType, "[]"))
		}
		return exportName(apiType)
	}
}

func rpcPackageAlias(importPath string) string {
	if importPath == "" {
		return ""
	}
	name := strings.Trim(importPath, "/")
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.ReplaceAll(name, "-", "_")
	if name == "" {
		return "pb"
	}
	return name
}
