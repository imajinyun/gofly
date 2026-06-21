package benchmarks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	hertzconfig "github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/hertz/pkg/common/ut"
	hertzroute "github.com/cloudwego/hertz/pkg/route"
	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gofiber/fiber/v2"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/rest"
	"github.com/labstack/echo/v4"
)

type httpBenchPayload struct {
	Name string `json:"name" validate:"required"`
}

func BenchmarkHTTPHello(b *testing.B) {
	for _, candidate := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "net_http", handler: newStdHTTPHelloHandler()},
		{name: "gofly", handler: newGoflyHelloHandler(false)},
		{name: "gin", handler: newGinHelloHandler(false)},
		{name: "echo", handler: newEchoHelloHandler(false)},
		{name: "chi", handler: newChiHelloHandler(false)},
	} {
		b.Run(candidate.name, func(b *testing.B) {
			benchHTTPHandler(b, candidate.handler, http.MethodGet, "/hello", nil)
		})
	}
	b.Run("fiber", func(b *testing.B) { benchFiberApp(b, newFiberHelloApp(false), http.MethodGet, "/hello", nil) })
	b.Run("hertz", func(b *testing.B) { benchHertzEngine(b, newHertzHelloEngine(false), http.MethodGet, "/hello", nil) })
}

func BenchmarkHTTPPathParams(b *testing.B) {
	for _, candidate := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "net_http", handler: newStdHTTPPathHandler()},
		{name: "gofly", handler: newGoflyPathHandler()},
		{name: "gin", handler: newGinPathHandler()},
		{name: "echo", handler: newEchoPathHandler()},
		{name: "chi", handler: newChiPathHandler()},
	} {
		b.Run(candidate.name, func(b *testing.B) {
			benchHTTPHandler(b, candidate.handler, http.MethodGet, "/users/42", nil)
		})
	}
	b.Run("fiber", func(b *testing.B) { benchFiberApp(b, newFiberPathApp(), http.MethodGet, "/users/42", nil) })
	b.Run("hertz", func(b *testing.B) { benchHertzEngine(b, newHertzPathEngine(), http.MethodGet, "/users/42", nil) })
}

func BenchmarkHTTPJSONBinding(b *testing.B) {
	body := []byte(`{"name":"ada"}`)
	for _, candidate := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "net_http", handler: newStdHTTPJSONHandler()},
		{name: "gofly", handler: newGoflyJSONHandler()},
		{name: "gin", handler: newGinJSONHandler()},
		{name: "echo", handler: newEchoJSONHandler()},
		{name: "chi", handler: newChiJSONHandler()},
	} {
		b.Run(candidate.name, func(b *testing.B) {
			benchHTTPHandler(b, candidate.handler, http.MethodPost, "/users", body)
		})
	}
	b.Run("fiber", func(b *testing.B) { benchFiberApp(b, newFiberJSONApp(), http.MethodPost, "/users", body) })
	b.Run("hertz", func(b *testing.B) { benchHertzEngine(b, newHertzJSONEngine(), http.MethodPost, "/users", body) })
}

func BenchmarkHTTPMiddlewareChain(b *testing.B) {
	for _, candidate := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "net_http", handler: withStdMiddlewareChain(newStdHTTPHelloHandler())},
		{name: "gofly", handler: newGoflyHelloHandler(true)},
		{name: "gin", handler: newGinHelloHandler(true)},
		{name: "echo", handler: newEchoHelloHandler(true)},
		{name: "chi", handler: newChiHelloHandler(true)},
	} {
		b.Run(candidate.name, func(b *testing.B) {
			benchHTTPHandler(b, candidate.handler, http.MethodGet, "/hello", nil)
		})
	}
	b.Run("fiber", func(b *testing.B) { benchFiberApp(b, newFiberHelloApp(true), http.MethodGet, "/hello", nil) })
	b.Run("hertz", func(b *testing.B) { benchHertzEngine(b, newHertzHelloEngine(true), http.MethodGet, "/hello", nil) })
}

func BenchmarkHTTPOpenAPI(b *testing.B) {
	for _, candidate := range []struct {
		name    string
		handler http.Handler
	}{
		{name: "disabled", handler: newGoflyHelloHandler(false)},
		{name: "enabled", handler: newGoflyOpenAPIHandler()},
	} {
		b.Run(candidate.name, func(b *testing.B) {
			benchHTTPHandler(b, candidate.handler, http.MethodGet, "/hello", nil)
		})
	}
}

func BenchmarkHTTPGovernance(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello")
	}))
	defer upstream.Close()

	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-rest",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/hello",
		Policy: governance.Policy{
			Headers: map[string]string{"X-Gofly-Benchmark": "enabled"},
		},
	})

	for _, candidate := range []struct {
		name   string
		client *rest.Client
	}{
		{name: "disabled", client: rest.MustNewClient(upstream.URL, rest.WithClientService("orders"))},
		{name: "enabled", client: rest.MustNewClient(upstream.URL, rest.WithClientService("orders"), rest.WithClientGovernanceRuleSet(rules))},
	} {
		b.Run(candidate.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				resp, err := candidate.client.Get(context.Background(), "/hello")
				if err != nil {
					b.Fatal(err)
				}
				closeResponse(resp)
			}
		})
	}
}

func benchHTTPHandler(b *testing.B, handler http.Handler, method, target string, body []byte) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}

func benchFiberApp(b *testing.B, app *fiber.App, method, target string, body []byte) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			b.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		closeResponse(resp)
	}
}

func benchHertzEngine(b *testing.B, engine *hertzroute.Engine, method, target string, body []byte) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		w := ut.PerformRequest(engine, method, target, &ut.Body{Body: bytes.NewBuffer(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"})
		resp := w.Result()
		if resp.StatusCode() != http.StatusOK {
			b.Fatalf("status = %d, want 200", resp.StatusCode())
		}
	}
}

func newStdHTTPHelloHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello")
	})
	return mux
}

func newStdHTTPPathHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.PathValue("id"))
	})
	return mux
}

func newStdHTTPJSONHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var payload httpBenchPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, payload.Name)
	})
	return mux
}

func newGoflyHelloHandler(withMiddleware bool) http.Handler {
	s := rest.MustNewServer(rest.Config{DisableDefaultMiddlewares: true})
	if withMiddleware {
		for _, mw := range goflyNoopMiddlewares() {
			s.Use(mw)
		}
	}
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: "/hello", Handler: func(ctx *rest.Context) { ctx.String(http.StatusOK, "hello") }})
	return s.Handler()
}

func newGoflyPathHandler() http.Handler {
	s := rest.MustNewServer(rest.Config{DisableDefaultMiddlewares: true})
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: "/users/{id}", Handler: func(ctx *rest.Context) { ctx.String(http.StatusOK, ctx.PathValue("id")) }})
	return s.Handler()
}

func newGoflyJSONHandler() http.Handler {
	s := rest.MustNewServer(rest.Config{DisableDefaultMiddlewares: true})
	s.AddRoute(rest.Route{Method: http.MethodPost, Path: "/users", Handler: func(ctx *rest.Context) {
		var payload httpBenchPayload
		if err := ctx.Bind(&payload); err != nil {
			ctx.Error(err)
			return
		}
		ctx.String(http.StatusOK, payload.Name)
	}})
	return s.Handler()
}

func newGoflyOpenAPIHandler() http.Handler {
	s := rest.MustNewServer(rest.Config{DisableDefaultMiddlewares: true})
	s.AddRoute(rest.Route{Method: http.MethodGet, Path: "/hello", Summary: "hello", Handler: func(ctx *rest.Context) { ctx.String(http.StatusOK, "hello") }})
	s.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "benchmark", Version: "v0"})
	return s.Handler()
}

func goflyNoopMiddlewares() []rest.Middleware {
	mws := make([]rest.Middleware, 5)
	for i := range mws {
		mws[i] = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
		}
	}
	return mws
}

func newGinHelloHandler(withMiddleware bool) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard
	r := gin.New()
	if withMiddleware {
		for i := 0; i < 5; i++ {
			r.Use(func(c *gin.Context) { c.Next() })
		}
	}
	r.GET("/hello", func(c *gin.Context) { c.String(http.StatusOK, "hello") })
	return r
}

func newGinPathHandler() http.Handler {
	r := gin.New()
	r.GET("/users/:id", func(c *gin.Context) { c.String(http.StatusOK, c.Param("id")) })
	return r
}

func newGinJSONHandler() http.Handler {
	r := gin.New()
	r.POST("/users", func(c *gin.Context) {
		var payload httpBenchPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.String(http.StatusOK, payload.Name)
	})
	return r
}

func newEchoHelloHandler(withMiddleware bool) http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Logger.SetOutput(io.Discard)
	if withMiddleware {
		for i := 0; i < 5; i++ {
			e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error { return next(c) }
			})
		}
	}
	e.GET("/hello", func(c echo.Context) error { return c.String(http.StatusOK, "hello") })
	return e
}

func newEchoPathHandler() http.Handler {
	e := echo.New()
	e.Logger.SetOutput(io.Discard)
	e.GET("/users/:id", func(c echo.Context) error { return c.String(http.StatusOK, c.Param("id")) })
	return e
}

func newEchoJSONHandler() http.Handler {
	e := echo.New()
	e.Logger.SetOutput(io.Discard)
	e.POST("/users", func(c echo.Context) error {
		var payload httpBenchPayload
		if err := c.Bind(&payload); err != nil {
			return c.String(http.StatusBadRequest, err.Error())
		}
		return c.String(http.StatusOK, payload.Name)
	})
	return e
}

func newChiHelloHandler(withMiddleware bool) http.Handler {
	r := chi.NewRouter()
	if withMiddleware {
		for i := 0; i < 5; i++ {
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
			})
		}
	}
	r.Get("/hello", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "hello") })
	return r
}

func newChiPathHandler() http.Handler {
	r := chi.NewRouter()
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, chi.URLParam(r, "id")) })
	return r
}

func newChiJSONHandler() http.Handler {
	r := chi.NewRouter()
	r.Post("/users", func(w http.ResponseWriter, r *http.Request) {
		var payload httpBenchPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, payload.Name)
	})
	return r
}

func newFiberHelloApp(withMiddleware bool) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	if withMiddleware {
		for i := 0; i < 5; i++ {
			app.Use(func(c *fiber.Ctx) error { return c.Next() })
		}
	}
	app.Get("/hello", func(c *fiber.Ctx) error { return c.SendString("hello") })
	return app
}

func newFiberPathApp() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/users/:id", func(c *fiber.Ctx) error { return c.SendString(c.Params("id")) })
	return app
}

func newFiberJSONApp() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/users", func(c *fiber.Ctx) error {
		var payload httpBenchPayload
		if err := c.BodyParser(&payload); err != nil {
			return c.Status(http.StatusBadRequest).SendString(err.Error())
		}
		return c.SendString(payload.Name)
	})
	return app
}

func newHertzHelloEngine(withMiddleware bool) *hertzroute.Engine {
	engine := hertzroute.NewEngine(hertzconfig.NewOptions([]hertzconfig.Option{}))
	if withMiddleware {
		for i := 0; i < 5; i++ {
			engine.Use(func(ctx context.Context, c *app.RequestContext) { c.Next(ctx) })
		}
	}
	engine.GET("/hello", func(ctx context.Context, c *app.RequestContext) { c.String(http.StatusOK, "hello") })
	return engine
}

func newHertzPathEngine() *hertzroute.Engine {
	engine := hertzroute.NewEngine(hertzconfig.NewOptions([]hertzconfig.Option{}))
	engine.GET("/users/:id", func(ctx context.Context, c *app.RequestContext) { c.String(http.StatusOK, c.Param("id")) })
	return engine
}

func newHertzJSONEngine() *hertzroute.Engine {
	engine := hertzroute.NewEngine(hertzconfig.NewOptions([]hertzconfig.Option{}))
	engine.POST("/users", func(ctx context.Context, c *app.RequestContext) {
		var payload httpBenchPayload
		if err := c.BindAndValidate(&payload); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.String(http.StatusOK, payload.Name)
	})
	return engine
}

func withStdMiddlewareChain(handler http.Handler) http.Handler {
	for i := 0; i < 5; i++ {
		next := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	}
	return handler
}

func closeResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	hlog.SetOutput(io.Discard)
	hlog.SetLevel(hlog.LevelFatal)
}
