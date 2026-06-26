package command

import (
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type aiProjectPlan struct {
	Prompt            string                    `json:"prompt"`
	ProjectType       string                    `json:"projectType"`
	Template          generator.ProjectTemplate `json:"template"`
	Features          []string                  `json:"features"`
	Command           string                    `json:"command"`
	RiskLevel         string                    `json:"riskLevel"`
	MutatesFilesystem bool                      `json:"mutatesFilesystem"`
	DryRun            bool                      `json:"dryRun"`
	Verify            []string                  `json:"verify"`
	Warnings          []string                  `json:"warnings,omitempty"`
	NextActions       []string                  `json:"nextActions"`
}

type aiProjectApplyResult struct {
	Plan              aiProjectPlan                    `json:"plan"`
	Applied           bool                             `json:"applied"`
	OutputDir         string                           `json:"outputDir"`
	ExecutedCommand   string                           `json:"executedCommand"`
	GeneratedFeatures []generator.ProjectFeatureResult `json:"generatedFeatures,omitempty"`
	Dependencies      []string                         `json:"dependencies,omitempty"`
	ConfigHints       []generator.ConfigHint           `json:"configHints,omitempty"`
	FeatureVerify     []string                         `json:"featureVerify,omitempty"`
	Verify            []string                         `json:"verify"`
	VerifyRan         bool                             `json:"verifyRan"`
	VerifyPassed      bool                             `json:"verifyPassed"`
	Verification      []aiProjectVerificationResult    `json:"verification,omitempty"`
	Warnings          []string                         `json:"warnings,omitempty"`
	NextActions       []string                         `json:"nextActions"`
	MutatesFilesystem bool                             `json:"mutatesFilesystem"`
}

type aiProjectVerificationResult struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

type aiProjectApplyOptions struct {
	Verify        bool
	VerifyTimeout time.Duration
}

func printAIProjectPlanText(projectPlan aiProjectPlan) {
	cliOutputfIf("template=%s kind=%s risk=%s\n", projectPlan.Template.ID, projectPlan.ProjectType, projectPlan.RiskLevel)
	cliOutputfIf("features=%s\n", strings.Join(projectPlan.Features, ","))
	cliOutputfIf("command=%s\n", projectPlan.Command)
	for _, warning := range projectPlan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
}
