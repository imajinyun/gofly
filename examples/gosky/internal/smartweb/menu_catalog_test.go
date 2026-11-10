package smartweb

import (
	"strings"
	"testing"
)

// The unmodified SmartAdmin client resolves every page route with
// `import.meta.glob('../views/**/**.vue')` and looks up `../views/<component>`
// verbatim. A component without the `.vue` suffix resolves to undefined and
// blanks the console after a refresh on that route. Every code-owned page
// component must therefore carry the `.vue` suffix.
func TestManagementNavigationPageComponentsHaveVueSuffix(t *testing.T) {
	t.Parallel()
	var bad []string
	for _, menu := range smartWebManagementNavigation() {
		if menu.MenuType != SmartWebMenuTypePage || menu.Path == "" || menu.Component == "" {
			continue
		}
		if !strings.HasSuffix(menu.Component, ".vue") {
			bad = append(bad, menu.Path+"="+menu.Component)
		}
	}
	if len(bad) != 0 {
		t.Fatalf("page menu components must end with .vue (SmartAdmin dynamic import): %v", bad)
	}
}
