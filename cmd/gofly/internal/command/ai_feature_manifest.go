package command

import (
	"sort"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func buildAIFeatureLibraryManifest() aiFeatureLibraryManifest {
	plugins := generator.ListProjectFeaturePluginContracts()
	return aiFeatureLibraryManifest{
		Mode:                 "deterministic built-in project feature plugins selected from project template feature tags",
		Deterministic:        true,
		AppliesUnderDirOnly:  true,
		DependencyPolicy:     "feature dependencies are reported in ai new apply results and nextActions for explicit review; they are not automatically added to the root module or generated go.mod",
		Features:             aiProjectFeatureNames(plugins),
		Templates:            aiProjectTemplateIDs(),
		VerifyAllowlist:      generator.ProjectFeatureVerifyAllowlist(),
		TemplateVerification: buildAITemplateVerificationContract(),
		ResultFields:         []string{"generatedFeatures", "dependencies", "configHints", "featureVerify", "verify", "nextActions"},
		Plugins:              plugins,
	}
}

func aiProjectFeatureNames(plugins []generator.ProjectFeaturePluginContract) []string {
	names := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		names = append(names, plugin.Name)
	}
	sort.Strings(names)
	return names
}

func aiProjectTemplateIDs() []string {
	templates := generator.ListProjectTemplates()
	ids := make([]string, 0, len(templates))
	for _, tmpl := range templates {
		ids = append(ids, tmpl.ID)
	}
	sort.Strings(ids)
	return ids
}

func buildAITemplateVerificationContract() aiTemplateVerificationContract {
	templates := generator.ListProjectTemplates()
	validated := make([]string, 0, len(templates))
	for _, tmpl := range templates {
		if tmpl.VerifyE2EValidated {
			validated = append(validated, tmpl.ID)
		}
	}
	return aiTemplateVerificationContract{
		CatalogField:       "verifyE2EValidated",
		MatrixTarget:       "make test-generated-matrix",
		GovernanceRound:    "generated project verification matrix",
		CIRequired:         true,
		ZeroSkipRequired:   true,
		ValidatedTemplates: validated,
	}
}
