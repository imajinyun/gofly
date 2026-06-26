package command

import (
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func filterProjectTemplates(templates []generator.ProjectTemplate, category, name string) []generator.ProjectTemplate {
	out := make([]generator.ProjectTemplate, 0, len(templates))
	for _, tmpl := range templates {
		if templateCatalogFilterMatch(tmpl, category, name) {
			out = append(out, tmpl)
		}
	}
	return out
}

func templateCatalogFilterMatch(tmpl generator.ProjectTemplate, category, name string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" && strings.ToLower(tmpl.ID) != name && !strings.Contains(strings.ToLower(tmpl.Name), name) {
		return false
	}
	if category == "" {
		return true
	}
	if strings.EqualFold(tmpl.Kind, category) || strings.EqualFold(tmpl.Language, category) || strings.EqualFold(tmpl.Architecture, category) {
		return true
	}
	for _, feature := range tmpl.Features {
		if strings.EqualFold(feature, category) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(tmpl.ID), category)
}

func templateFilterMatch(templateName, category, name string) bool {
	templateName = strings.ToLower(strings.TrimSpace(templateName))
	category = strings.ToLower(strings.TrimSpace(category))
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" && templateName != name && strings.TrimSuffix(templateName, filepath.Ext(templateName)) != name {
		return false
	}
	if category == "" {
		return true
	}
	switch category {
	case "api", "rpc", "model", "docker":
		return strings.HasPrefix(templateName, category)
	case "kube", "kubernetes":
		return strings.HasPrefix(templateName, "kube")
	default:
		return strings.Contains(templateName, category)
	}
}
