package rest_test

import (
	"fmt"
	"net/http"

	"github.com/imajinyun/gofly/rest"
)

// ExampleServer demonstrates wiring a minimal REST server with a JSON route
// and reading back the route specs that drive the OpenAPI contract.
func ExampleServer() {
	srv := rest.MustNewServer(rest.Config{Name: "users"})
	srv.AddRoute(rest.Route{
		Method:    http.MethodGet,
		Path:      "/users/{id}",
		Summary:   "Fetch a user by id",
		Tags:      []string{"users"},
		Responses: map[string]rest.Response{"200": rest.JSONResponse("the user", rest.Schema{Type: "object"})},
		Handler: func(c *rest.Context) {
			c.JSON(http.StatusOK, map[string]string{"id": c.PathValue("id")})
		},
	})

	for _, route := range srv.Routes() {
		fmt.Printf("%s %s\n", route.Method, route.Path)
	}
	// Output:
	// GET /users/{id}
}

// ExampleServer_AddOpenAPIRoutes shows that mounting the contract endpoints
// exposes /openapi.json and /docs alongside the application routes.
func ExampleServer_AddOpenAPIRoutes() {
	srv := rest.MustNewServer(rest.Config{Name: "users"})
	srv.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/ping",
		Handler: func(c *rest.Context) { c.String(http.StatusOK, "pong") },
	})
	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "users API", Version: "1.0.0"})

	doc := srv.OpenAPI(rest.OpenAPIInfo{Title: "users API", Version: "1.0.0"})
	fmt.Println(doc.OpenAPI)
	fmt.Println(doc.Info.Title, doc.Info.Version)
	_, hasContract := doc.Paths["/openapi.json"]
	_, hasDocs := doc.Paths["/docs"]
	fmt.Println(hasContract, hasDocs)
	// Output:
	// 3.0.3
	// users API 1.0.0
	// true true
}
