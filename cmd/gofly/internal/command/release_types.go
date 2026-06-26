package command

// releaseCheckReport aggregates all release-governance signals into a single
// structured report that can be consumed by CI or release automation.
type releaseCheckReport struct {
	Version     string             `json:"version"`
	Recommended string             `json:"recommended_semver"`
	Blocking    []string           `json:"blocking"`
	Warnings    []string           `json:"warnings"`
	Checks      []releaseCheckItem `json:"checks"`
	Summary     string             `json:"summary"`
}

type releaseCheckItem struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // pass / fail / skip
	Detail  string `json:"detail,omitempty"`
	Blocker bool   `json:"blocker"`
}
