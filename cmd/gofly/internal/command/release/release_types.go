package release

// CheckReport is the exported release check report used by the command adapter.
type CheckReport = releaseCheckReport

// CheckItem is a single named check in a release report.
type CheckItem = releaseCheckItem

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
	Name     string         `json:"name"`
	Status   string         `json:"status"` // pass / fail / skip
	Detail   string         `json:"detail,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty"`
	Blocker  bool           `json:"blocker"`
}
