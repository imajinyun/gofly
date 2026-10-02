package generator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const apiPerformanceManifestSchema = "gofly.api_performance_fixtures.v1"

var benchmarkAPIDocumentSink IDLDocument

type apiPerformanceManifest struct {
	Schema   string                  `json:"schema"`
	Fixtures []apiPerformanceFixture `json:"fixtures"`
}

type apiPerformanceFixture struct {
	Name                       string `json:"name"`
	File                       string `json:"file"`
	RouteCount                 int    `json:"routeCount"`
	TypeCount                  int    `json:"typeCount"`
	ImportCount                int    `json:"importCount"`
	SHA256                     string `json:"sha256"`
	ExpectedGeneratedFileCount int    `json:"expectedGeneratedFileCount"`
}

type apiPerformanceFixtureGraph struct {
	Files       map[string][]byte
	ImportCount int
	SHA256      string
}

type apiPerformanceGeneratedOutput struct {
	Files map[string][]byte
	Bytes int64
}

func TestAPIPerformanceFixtures(t *testing.T) {
	root := apiPerformanceFixtureRoot(t)
	manifest := loadAPIPerformanceManifest(t, root)
	wantFixtures := []struct {
		name   string
		routes int
	}{
		{name: "small", routes: 20},
		{name: "medium", routes: 200},
		{name: "large", routes: 1000},
	}
	if len(manifest.Fixtures) != len(wantFixtures) {
		t.Fatalf("manifest fixtures = %d, want %d", len(manifest.Fixtures), len(wantFixtures))
	}

	seenNames := make(map[string]struct{}, len(manifest.Fixtures))
	seenRoutes := make(map[int]struct{}, len(manifest.Fixtures))
	for index, fixture := range manifest.Fixtures {
		want := wantFixtures[index]
		if fixture.Name != want.name || fixture.RouteCount != want.routes {
			t.Fatalf("manifest fixture[%d] = %q/%d routes, want %q/%d", index, fixture.Name, fixture.RouteCount, want.name, want.routes)
		}
		if _, ok := seenNames[fixture.Name]; ok {
			t.Fatalf("manifest contains duplicate fixture name %q", fixture.Name)
		}
		if _, ok := seenRoutes[fixture.RouteCount]; ok {
			t.Fatalf("manifest contains duplicate route tier %d", fixture.RouteCount)
		}
		seenNames[fixture.Name] = struct{}{}
		seenRoutes[fixture.RouteCount] = struct{}{}

		t.Run(fixture.Name, func(t *testing.T) {
			graph, doc := loadAndValidateAPIPerformanceFixture(t, root, fixture)
			assertAPIPerformanceFeatureCoverage(t, graph, doc)

			firstRoot := t.TempDir()
			first := generateAndReadAPIPerformanceOutput(t, filepath.Join(root, filepath.FromSlash(fixture.File)), firstRoot)
			if got := len(first.Files); got != fixture.ExpectedGeneratedFileCount {
				t.Fatalf("generated files = %d, want %d", got, fixture.ExpectedGeneratedFileCount)
			}
			second := generateAndReadAPIPerformanceOutput(t, filepath.Join(root, filepath.FromSlash(fixture.File)), t.TempDir())
			if !reflect.DeepEqual(first.Files, second.Files) {
				t.Fatal("repeated REST generation is not byte deterministic")
			}
			assertAPIPerformanceOpenAPIAndClients(t, root, fixture, doc)
		})
	}
}

func BenchmarkAPIParse(b *testing.B) {
	root := apiPerformanceFixtureRoot(b)
	manifest := loadAPIPerformanceManifest(b, root)
	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		b.Run(fmt.Sprintf("routes=%d", fixture.RouteCount), func(b *testing.B) {
			graph, merged := loadAndValidateAPIPerformanceFixture(b, root, fixture)
			source, ok := graph.Files[filepath.ToSlash(fixture.File)]
			if !ok {
				b.Fatalf("fixture graph does not contain root %q", fixture.File)
			}
			input := string(source)
			parsed, err := ParseAPI(input)
			if err != nil {
				b.Fatalf("ParseAPI correctness check: %v", err)
			}
			if got := apiPerformanceRouteCount(parsed); got != fixture.RouteCount {
				b.Fatalf("ParseAPI root routes = %d, want %d", got, fixture.RouteCount)
			}
			if got := apiPerformanceRouteCount(merged); got != fixture.RouteCount {
				b.Fatalf("LoadAPI merged routes = %d, want %d", got, fixture.RouteCount)
			}

			b.ReportAllocs()
			for b.Loop() {
				doc, parseErr := ParseAPI(input)
				if parseErr != nil {
					b.Fatal(parseErr)
				}
				benchmarkAPIDocumentSink = doc
			}
			b.ReportMetric(float64(len(source)), "input-bytes/op")
		})
	}
}

func BenchmarkAPIGenerate(b *testing.B) {
	root := apiPerformanceFixtureRoot(b)
	manifest := loadAPIPerformanceManifest(b, root)
	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		b.Run(fmt.Sprintf("routes=%d", fixture.RouteCount), func(b *testing.B) {
			_, doc := loadAndValidateAPIPerformanceFixture(b, root, fixture)
			first := generateAndReadAPIPerformanceDocument(b, doc, b.TempDir())
			second := generateAndReadAPIPerformanceDocument(b, doc, b.TempDir())
			if !reflect.DeepEqual(first.Files, second.Files) {
				b.Fatal("REST benchmark preflight is not byte deterministic")
			}
			if got := len(first.Files); got != fixture.ExpectedGeneratedFileCount {
				b.Fatalf("REST benchmark preflight files = %d, want %d", got, fixture.ExpectedGeneratedFileCount)
			}

			benchmarkRoot := b.TempDir()
			iteration := 0
			b.ReportAllocs()
			for b.Loop() {
				output := filepath.Join(benchmarkRoot, "iteration-"+strconv.Itoa(iteration))
				if err := writeRESTFiles(doc, APIOptions{Dir: output, Package: "api"}); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := os.RemoveAll(output); err != nil {
					b.Fatalf("remove benchmark output: %v", err)
				}
				iteration++
				b.StartTimer()
			}
			b.ReportMetric(float64(first.Bytes), "bytes/op")
			b.ReportMetric(float64(len(first.Files)), "files/op")
		})
	}
}

func BenchmarkAPIOpenAPIExport(b *testing.B) {
	root := apiPerformanceFixtureRoot(b)
	manifest := loadAPIPerformanceManifest(b, root)
	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		b.Run(fmt.Sprintf("routes=%d", fixture.RouteCount), func(b *testing.B) {
			_, doc := loadAndValidateAPIPerformanceFixture(b, root, fixture)
			first := generateAndReadAPIPerformanceOpenAPI(b, doc, b.TempDir(), fixture.RouteCount)
			second := generateAndReadAPIPerformanceOpenAPI(b, doc, b.TempDir(), fixture.RouteCount)
			if !reflect.DeepEqual(first.Files, second.Files) {
				b.Fatal("OpenAPI export benchmark preflight is not byte deterministic")
			}

			benchmarkRoot := b.TempDir()
			iteration := 0
			b.ReportAllocs()
			for b.Loop() {
				outputRoot := filepath.Join(benchmarkRoot, "iteration-"+strconv.Itoa(iteration))
				data, err := generateAPIOpenAPI(doc)
				if err != nil {
					b.Fatal(err)
				}
				if err := writeGeneratedFile(filepath.Join(outputRoot, "openapi.json"), data); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := os.RemoveAll(outputRoot); err != nil {
					b.Fatalf("remove OpenAPI export output: %v", err)
				}
				iteration++
				b.StartTimer()
			}
			b.ReportMetric(float64(first.Bytes), "bytes/op")
			b.ReportMetric(float64(len(first.Files)), "files/op")
		})
	}
}

func BenchmarkAPIOpenAPIImport(b *testing.B) {
	root := apiPerformanceFixtureRoot(b)
	manifest := loadAPIPerformanceManifest(b, root)
	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		b.Run(fmt.Sprintf("routes=%d", fixture.RouteCount), func(b *testing.B) {
			_, doc := loadAndValidateAPIPerformanceFixture(b, root, fixture)
			openAPI, err := generateAPIOpenAPI(doc)
			if err != nil {
				b.Fatalf("prepare OpenAPI import source: %v", err)
			}
			first := generateAndReadAPIPerformanceImport(b, openAPI, b.TempDir(), fixture.RouteCount)
			second := generateAndReadAPIPerformanceImport(b, openAPI, b.TempDir(), fixture.RouteCount)
			if !reflect.DeepEqual(first.Files, second.Files) {
				b.Fatal("OpenAPI import benchmark preflight is not byte deterministic")
			}

			benchmarkRoot := b.TempDir()
			iteration := 0
			b.ReportAllocs()
			for b.Loop() {
				outputRoot := filepath.Join(benchmarkRoot, "iteration-"+strconv.Itoa(iteration))
				if err := writeAPIPerformanceImport(openAPI, filepath.Join(outputRoot, "imported.api")); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := os.RemoveAll(outputRoot); err != nil {
					b.Fatalf("remove OpenAPI import output: %v", err)
				}
				iteration++
				b.StartTimer()
			}
			b.ReportMetric(float64(first.Bytes), "bytes/op")
			b.ReportMetric(float64(len(first.Files)), "files/op")
		})
	}
}

func BenchmarkAPIClientGenerate(b *testing.B) {
	root := apiPerformanceFixtureRoot(b)
	manifest := loadAPIPerformanceManifest(b, root)
	for _, language := range apiPerformanceClientLanguages() {
		language := language
		b.Run(language, func(b *testing.B) {
			for _, fixture := range manifest.Fixtures {
				fixture := fixture
				b.Run(fmt.Sprintf("routes=%d", fixture.RouteCount), func(b *testing.B) {
					_, doc := loadAndValidateAPIPerformanceFixture(b, root, fixture)
					doc = apiDocumentWithResolvedInlineFields(doc)
					first := generateAndReadAPIPerformanceClient(b, doc, language, b.TempDir())
					second := generateAndReadAPIPerformanceClient(b, doc, language, b.TempDir())
					if !reflect.DeepEqual(first.Files, second.Files) {
						b.Fatalf("%s client benchmark preflight is not byte deterministic", language)
					}

					benchmarkRoot := b.TempDir()
					iteration := 0
					b.ReportAllocs()
					for b.Loop() {
						outputRoot := filepath.Join(benchmarkRoot, "iteration-"+strconv.Itoa(iteration))
						output := filepath.Join(outputRoot, apiPerformanceClientFile(language))
						if err := writeAPIPerformanceClient(doc, language, output); err != nil {
							b.Fatal(err)
						}
						b.StopTimer()
						if err := os.RemoveAll(outputRoot); err != nil {
							b.Fatalf("remove %s client output: %v", language, err)
						}
						iteration++
						b.StartTimer()
					}
					b.ReportMetric(float64(first.Bytes), "bytes/op")
					b.ReportMetric(float64(len(first.Files)), "files/op")
				})
			}
		})
	}
}

func assertAPIPerformanceOpenAPIAndClients(
	t *testing.T,
	fixtureRoot string,
	fixture apiPerformanceFixture,
	doc IDLDocument,
) {
	t.Helper()
	apiPath := filepath.Join(fixtureRoot, filepath.FromSlash(fixture.File))
	t.Run("openapi", func(t *testing.T) {
		firstRoot := t.TempDir()
		first := generateAndReadAPIPerformanceOpenAPIFromFile(t, apiPath, firstRoot, fixture.RouteCount)
		second := generateAndReadAPIPerformanceOpenAPIFromFile(t, apiPath, t.TempDir(), fixture.RouteCount)
		if !reflect.DeepEqual(first.Files, second.Files) {
			t.Fatal("repeated OpenAPI export is not byte deterministic")
		}

		openAPIPath := filepath.Join(firstRoot, "openapi.json")
		firstImport := generateAndReadAPIPerformanceImportFromFile(t, openAPIPath, t.TempDir(), fixture.RouteCount)
		secondImport := generateAndReadAPIPerformanceImportFromFile(t, openAPIPath, t.TempDir(), fixture.RouteCount)
		if !reflect.DeepEqual(firstImport.Files, secondImport.Files) {
			t.Fatal("repeated OpenAPI import is not byte deterministic")
		}
	})

	for _, language := range apiPerformanceClientLanguages() {
		language := language
		t.Run("client/"+language, func(t *testing.T) {
			first := generateAndReadAPIPerformanceClientFromFile(t, apiPath, language, t.TempDir())
			second := generateAndReadAPIPerformanceClientFromFile(t, apiPath, language, t.TempDir())
			if !reflect.DeepEqual(first.Files, second.Files) {
				t.Fatalf("repeated %s client generation is not byte deterministic", language)
			}
		})
	}

	openAPI := generateAndReadAPIPerformanceOpenAPI(t, doc, t.TempDir(), fixture.RouteCount)
	if len(openAPI.Files) != 1 {
		t.Fatalf("in-memory OpenAPI preflight files = %d, want 1", len(openAPI.Files))
	}
}

func generateAndReadAPIPerformanceOpenAPIFromFile(
	tb testing.TB,
	apiPath string,
	outputRoot string,
	routeCount int,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	output := filepath.Join(outputRoot, "openapi.json")
	if err := GenerateAPIDoc(APIDocOptions{APIFile: apiPath, Output: output, Format: "openapi"}); err != nil {
		tb.Fatalf("GenerateAPIDoc: %v", err)
	}
	generated := readAPIPerformanceOutputFiles(tb, outputRoot)
	assertAPIPerformanceOpenAPI(tb, generated, routeCount)
	return generated
}

func generateAndReadAPIPerformanceOpenAPI(
	tb testing.TB,
	doc IDLDocument,
	outputRoot string,
	routeCount int,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	data, err := generateAPIOpenAPI(doc)
	if err != nil {
		tb.Fatalf("generateAPIOpenAPI: %v", err)
	}
	if err := writeGeneratedFile(filepath.Join(outputRoot, "openapi.json"), data); err != nil {
		tb.Fatalf("write OpenAPI performance output: %v", err)
	}
	generated := readAPIPerformanceOutputFiles(tb, outputRoot)
	assertAPIPerformanceOpenAPI(tb, generated, routeCount)
	return generated
}

func assertAPIPerformanceOpenAPI(
	tb testing.TB,
	output apiPerformanceGeneratedOutput,
	routeCount int,
) {
	tb.Helper()
	if len(output.Files) != 1 {
		tb.Fatalf("OpenAPI generated files = %d, want 1", len(output.Files))
	}
	data, ok := output.Files["openapi.json"]
	if !ok || len(data) == 0 {
		tb.Fatal("OpenAPI output is missing or empty")
	}
	var spec struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		tb.Fatalf("decode OpenAPI output: %v", err)
	}
	if spec.OpenAPI == "" {
		tb.Fatal("OpenAPI output is missing its version")
	}
	if got := len(spec.Paths); got != routeCount {
		tb.Fatalf("OpenAPI paths = %d, want %d", got, routeCount)
	}
}

func generateAndReadAPIPerformanceImportFromFile(
	tb testing.TB,
	openAPIPath string,
	outputRoot string,
	routeCount int,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	output := filepath.Join(outputRoot, "imported.api")
	if err := GenerateAPIFromOpenAPI(APIImportOptions{
		Source:  openAPIPath,
		Output:  output,
		Service: "performance-api",
	}); err != nil {
		tb.Fatalf("GenerateAPIFromOpenAPI: %v", err)
	}
	generated := readAPIPerformanceOutputFiles(tb, outputRoot)
	assertAPIPerformanceImportedAPI(tb, generated, routeCount)
	return generated
}

func generateAndReadAPIPerformanceImport(
	tb testing.TB,
	openAPI []byte,
	outputRoot string,
	routeCount int,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	if err := writeAPIPerformanceImport(openAPI, filepath.Join(outputRoot, "imported.api")); err != nil {
		tb.Fatalf("write OpenAPI import performance output: %v", err)
	}
	generated := readAPIPerformanceOutputFiles(tb, outputRoot)
	assertAPIPerformanceImportedAPI(tb, generated, routeCount)
	return generated
}

func writeAPIPerformanceImport(openAPI []byte, output string) error {
	doc, err := parseOpenAPIToIDL(openAPI, "performance-api")
	if err != nil {
		return err
	}
	return writeGeneratedFile(output, FormatAPI(doc))
}

func assertAPIPerformanceImportedAPI(
	tb testing.TB,
	output apiPerformanceGeneratedOutput,
	routeCount int,
) {
	tb.Helper()
	if len(output.Files) != 1 {
		tb.Fatalf("OpenAPI import files = %d, want 1", len(output.Files))
	}
	data, ok := output.Files["imported.api"]
	if !ok || len(data) == 0 {
		tb.Fatal("OpenAPI import output is missing or empty")
	}
	doc, err := ParseAPI(string(data))
	if err != nil {
		tb.Fatalf("parse imported API: %v", err)
	}
	if err := ValidateAPI(doc); err != nil {
		tb.Fatalf("validate imported API: %v", err)
	}
	if got := apiPerformanceRouteCount(doc); got != routeCount {
		tb.Fatalf("imported API routes = %d, want %d", got, routeCount)
	}
}

func apiPerformanceClientLanguages() []string {
	return []string{"javascript", "typescript", "java", "kotlin", "dart"}
}

func apiPerformanceClientFile(language string) string {
	switch language {
	case "javascript":
		return "performance_client.js"
	case "typescript":
		return "performance_client.ts"
	case "java":
		return "APIClient.java"
	case "kotlin":
		return "APIClient.kt"
	case "dart":
		return "performance_client.dart"
	default:
		return "unsupported"
	}
}

func generateAndReadAPIPerformanceClientFromFile(
	tb testing.TB,
	apiPath string,
	language string,
	outputRoot string,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	output := filepath.Join(outputRoot, apiPerformanceClientFile(language))
	if err := GenerateAPIClient(APIClientOptions{
		APIFile:  apiPath,
		Output:   output,
		Language: language,
		BaseURL:  "https://api.example.test",
	}); err != nil {
		tb.Fatalf("GenerateAPIClient(%s): %v", language, err)
	}
	generated := readAPIPerformanceOutputFiles(tb, outputRoot)
	assertAPIPerformanceClient(tb, generated, language)
	return generated
}

func generateAndReadAPIPerformanceClient(
	tb testing.TB,
	doc IDLDocument,
	language string,
	outputRoot string,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	output := filepath.Join(outputRoot, apiPerformanceClientFile(language))
	if err := writeAPIPerformanceClient(doc, language, output); err != nil {
		tb.Fatalf("write %s API client: %v", language, err)
	}
	generated := readAPIPerformanceOutputFiles(tb, outputRoot)
	assertAPIPerformanceClient(tb, generated, language)
	return generated
}

func writeAPIPerformanceClient(doc IDLDocument, language string, output string) error {
	var data []byte
	switch language {
	case "javascript":
		data = generateJavaScriptClient(doc, "https://api.example.test")
	case "typescript":
		data = generateTypeScriptClient(doc, "https://api.example.test")
	case "java":
		data = generateJavaClient(doc, "https://api.example.test")
	case "kotlin":
		data = generateKotlinClient(doc, "https://api.example.test")
	case "dart":
		data = generateDartClient(doc, "https://api.example.test")
	default:
		return fmt.Errorf("unsupported API performance client language %q", language)
	}
	return writeGeneratedFile(output, data)
}

func assertAPIPerformanceClient(
	tb testing.TB,
	output apiPerformanceGeneratedOutput,
	language string,
) {
	tb.Helper()
	if len(output.Files) != 1 {
		tb.Fatalf("%s client generated files = %d, want 1", language, len(output.Files))
	}
	data, ok := output.Files[apiPerformanceClientFile(language)]
	if !ok || len(data) == 0 {
		tb.Fatalf("%s client output is missing or empty", language)
	}
	marker := "class APIClient"
	if language == "java" {
		marker = "public class APIClient"
	}
	if !bytes.Contains(data, []byte(marker)) {
		tb.Fatalf("%s client output is missing %q", language, marker)
	}
}

func apiPerformanceFixtureRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("resolve api performance fixture source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "bench", "testdata", "api-generator"))
}

func loadAPIPerformanceManifest(tb testing.TB, root string) apiPerformanceManifest {
	tb.Helper()
	data, err := ReadFileUnderRoot(root, "manifest.json", "api performance manifest")
	if err != nil {
		tb.Fatalf("read API performance manifest: %v", err)
	}
	var manifest apiPerformanceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		tb.Fatalf("decode API performance manifest: %v", err)
	}
	if manifest.Schema != apiPerformanceManifestSchema {
		tb.Fatalf("manifest schema = %q, want %q", manifest.Schema, apiPerformanceManifestSchema)
	}
	return manifest
}

func loadAndValidateAPIPerformanceFixture(
	tb testing.TB,
	root string,
	fixture apiPerformanceFixture,
) (apiPerformanceFixtureGraph, IDLDocument) {
	tb.Helper()
	graph, err := readAPIPerformanceFixtureGraph(root, fixture.File)
	if err != nil {
		tb.Fatalf("read fixture graph %q: %v", fixture.Name, err)
	}
	if graph.ImportCount != fixture.ImportCount {
		tb.Fatalf("%s physical imports = %d, want %d", fixture.Name, graph.ImportCount, fixture.ImportCount)
	}
	if graph.SHA256 != fixture.SHA256 {
		tb.Fatalf("%s graph sha256 = %s, want %s", fixture.Name, graph.SHA256, fixture.SHA256)
	}
	apiPath := filepath.Join(root, filepath.FromSlash(fixture.File))
	doc, err := LoadAPI(apiPath)
	if err != nil {
		tb.Fatalf("LoadAPI(%s): %v", fixture.Name, err)
	}
	if err := ValidateAPI(doc); err != nil {
		tb.Fatalf("ValidateAPI(%s): %v", fixture.Name, err)
	}
	if got := apiPerformanceRouteCount(doc); got != fixture.RouteCount {
		tb.Fatalf("%s merged routes = %d, want %d", fixture.Name, got, fixture.RouteCount)
	}
	if got := len(doc.Messages); got != fixture.TypeCount {
		tb.Fatalf("%s merged types = %d, want %d", fixture.Name, got, fixture.TypeCount)
	}
	return graph, doc
}

func readAPIPerformanceFixtureGraph(root, entry string) (apiPerformanceFixtureGraph, error) {
	files := make(map[string][]byte)
	importCount := 0
	var visit func(string) error
	visit = func(path string) error {
		absPath, err := SafeTarget(root, path, "api performance fixture")
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			return fmt.Errorf("resolve fixture relative path: %w", err)
		}
		rel = filepath.ToSlash(rel)
		if _, ok := files[rel]; ok {
			return nil
		}
		if filepath.Ext(rel) != ".api" {
			return fmt.Errorf("fixture import %q must reference .api file", rel)
		}
		data, err := ReadFileUnderRoot(root, filepath.FromSlash(rel), "api performance fixture")
		if err != nil {
			return err
		}
		files[rel] = data
		imports := apiImportPaths(string(data))
		importCount += len(imports)
		for _, imported := range imports {
			target, err := resolveAPIImport(absPath, imported)
			if err != nil {
				return err
			}
			if _, err := SafeTarget(root, target, "api performance fixture graph"); err != nil {
				return err
			}
			if err := visit(target); err != nil {
				return err
			}
		}
		return nil
	}

	if err := visit(entry); err != nil {
		return apiPerformanceFixtureGraph{}, err
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(files[path])
		_, _ = hash.Write([]byte{0})
	}
	return apiPerformanceFixtureGraph{
		Files:       files,
		ImportCount: importCount,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func assertAPIPerformanceFeatureCoverage(
	tb testing.TB,
	graph apiPerformanceFixtureGraph,
	doc IDLDocument,
) {
	tb.Helper()
	for _, path := range []string{"types/common.api", "types/nested/metadata.api"} {
		if _, ok := graph.Files[path]; !ok {
			tb.Fatalf("fixture graph missing nested import %q", path)
		}
	}
	if len(graph.Files) != 3 {
		tb.Fatalf("fixture graph files = %d, want 3", len(graph.Files))
	}
	for _, name := range []string{
		"AuditMetadata",
		"Money",
		"LineItem",
		"Payload",
		"PageRequest",
		"PageResponse",
		"ErrorEnvelope",
		"PerformanceRequest",
		"PerformanceResponse",
	} {
		if apiPerformanceMessage(doc, name) == nil {
			tb.Fatalf("merged document missing type %q", name)
		}
	}

	request := apiPerformanceMessage(doc, "PerformanceRequest")
	requiredFields := map[string]struct {
		typ string
		tag string
	}{
		"RequestID": {typ: "string", tag: `header:"X-Request-ID"`},
		"TenantID":  {typ: "string", tag: `validate:"required"`},
		"ID":        {typ: "string", tag: `path:"id"`},
		"Query":     {typ: "string", tag: `form:"q,optional"`},
		"Payload":   {typ: "*Payload", tag: `json:"payload,optional"`},
		"Items":     {typ: "[]*LineItem", tag: `validate:"omitempty,min=1"`},
		"Labels":    {typ: "map[string]string", tag: `json:"labels,optional"`},
		"Buckets":   {typ: "map[string][]*LineItem", tag: `json:"buckets,optional"`},
	}
	for name, want := range requiredFields {
		field := apiPerformanceField(request, name)
		if field == nil {
			tb.Fatalf("PerformanceRequest missing field %q", name)
			continue
		}
		if field.Type != want.typ || !strings.Contains(field.Tag, want.tag) {
			tb.Fatalf("PerformanceRequest.%s = type %q tag %q, want type %q containing %q", name, field.Type, field.Tag, want.typ, want.tag)
		}
	}

	groups := make(map[string]struct{}, len(doc.Services))
	successStatuses := make(map[int]struct{})
	errorStatuses := make(map[int]struct{})
	for _, service := range doc.Services {
		groups[service.Server.Group] = struct{}{}
		if service.Server.JWT == "" {
			tb.Fatalf("service %q is missing JWT metadata", service.Name)
		}
		if len(service.Server.Middleware) == 0 {
			tb.Fatalf("service %q is missing middleware metadata", service.Name)
		}
		for _, method := range service.Methods {
			successStatuses[apiSuccessStatus(method)] = struct{}{}
			for status := range apiResponseDescriptions(method) {
				if status >= 400 {
					errorStatuses[status] = struct{}{}
				}
			}
		}
	}
	for _, group := range []string{"public", "admin", "partner", "internal"} {
		if _, ok := groups[group]; !ok {
			tb.Fatalf("merged document missing route group %q", group)
		}
	}
	for _, status := range []int{200, 201, 202, 204} {
		if _, ok := successStatuses[status]; !ok {
			tb.Fatalf("merged document missing success response %d", status)
		}
	}
	for _, status := range []int{400, 401, 404, 409} {
		if _, ok := errorStatuses[status]; !ok {
			tb.Fatalf("merged document missing error response %d", status)
		}
	}
}

func apiPerformanceRouteCount(doc IDLDocument) int {
	count := 0
	for _, service := range doc.Services {
		count += len(service.Methods)
	}
	return count
}

func apiPerformanceMessage(doc IDLDocument, name string) *IDLMessage {
	for index := range doc.Messages {
		if doc.Messages[index].Name == name {
			return &doc.Messages[index]
		}
	}
	return nil
}

func apiPerformanceField(message *IDLMessage, name string) *IDLField {
	if message == nil {
		return nil
	}
	for index := range message.Fields {
		if message.Fields[index].Name == name {
			return &message.Fields[index]
		}
	}
	return nil
}

func generateAndReadAPIPerformanceOutput(
	tb testing.TB,
	apiPath string,
	outputRoot string,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	if err := GenerateRESTFromAPI(APIOptions{APIFile: apiPath, Dir: outputRoot, Package: "api"}); err != nil {
		tb.Fatalf("GenerateRESTFromAPI: %v", err)
	}
	return readAPIPerformanceGeneratedOutput(tb, outputRoot)
}

func generateAndReadAPIPerformanceDocument(
	tb testing.TB,
	doc IDLDocument,
	outputRoot string,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	if err := writeRESTFiles(doc, APIOptions{Dir: outputRoot, Package: "api"}); err != nil {
		tb.Fatalf("writeRESTFiles: %v", err)
	}
	return readAPIPerformanceGeneratedOutput(tb, outputRoot)
}

func readAPIPerformanceGeneratedOutput(
	tb testing.TB,
	root string,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	output := readAPIPerformanceOutputFiles(tb, root)
	for rel, data := range output.Files {
		if filepath.Ext(rel) != ".go" {
			tb.Fatalf("generated output %q is not a Go file", rel)
		}
		formatted, err := format.Source(data)
		if err != nil {
			tb.Fatalf("generated output %q is not valid Go: %v", rel, err)
		}
		if !bytes.Equal(data, formatted) {
			tb.Fatalf("generated output %q is not gofmt-clean", rel)
		}
	}
	return output
}

func readAPIPerformanceOutputFiles(
	tb testing.TB,
	root string,
) apiPerformanceGeneratedOutput {
	tb.Helper()
	output := apiPerformanceGeneratedOutput{Files: make(map[string][]byte)}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated output contains symlink %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("generated output contains non-regular file %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("generated output %q escapes root", rel)
		}
		data, err := ReadFileUnderRoot(root, filepath.FromSlash(rel), "api performance generated output")
		if err != nil {
			return err
		}
		output.Files[rel] = data
		output.Bytes += int64(len(data))
		return nil
	})
	if err != nil {
		tb.Fatalf("read generated output: %v", err)
	}
	return output
}
