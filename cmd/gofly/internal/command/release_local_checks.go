package command

import (
	"fmt"
	"os/exec"
	"strings"
)

func releaseGoAPICompatCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "go-api-compat", Status: "pass"}
	if out, err := runAPIDiffCheck(); err != nil {
		item.Status = "fail"
		item.Detail = string(out)
		item.Blocker = true
		return item, []string{"Go public API incompatible changes detected"}
	} else {
		item.Detail = strings.TrimSpace(string(out))
		if item.Detail == "" {
			item.Detail = "no incompatible changes"
		}
	}
	return item, nil
}

func releaseChangelogVersionCheck(path string) (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "changelog-version", Status: "pass"}
	changelogVersion, err := parseChangelogVersion(path)
	if err != nil {
		item.Status = "skip"
		item.Detail = "changelog not found or unparsable"
	} else if changelogVersion != "" && changelogVersion != Version {
		item.Status = "fail"
		item.Detail = fmt.Sprintf("CHANGELOG version %q != gofly version %q", changelogVersion, Version)
		item.Blocker = true
		return item, []string{item.Detail}
	} else {
		item.Detail = fmt.Sprintf("version %q", changelogVersion)
	}
	return item, nil
}

func releaseGoModTidyCheck() (releaseCheckItem, []string) {
	item := releaseCheckItem{Name: "go-mod-tidy", Status: "pass"}
	if out, err := exec.Command("go", "mod", "tidy", "-diff").CombinedOutput(); err != nil {
		item.Status = "fail"
		item.Detail = strings.TrimSpace(string(out))
		item.Blocker = true
		return item, []string{"go mod tidy would change go.mod/go.sum"}
	}
	item.Detail = "clean"
	return item, nil
}
