package command

import "flag"

type templateSourceFlags struct {
	Home   *string
	Remote *string
	Branch *string
}

func registerTemplateSourceFlags(fs *flag.FlagSet, homeUsage, remoteUsage, branchUsage string) templateSourceFlags {
	if homeUsage == "" {
		homeUsage = "template home directory"
	}
	if remoteUsage == "" {
		remoteUsage = "remote template repository"
	}
	if branchUsage == "" {
		branchUsage = "remote template branch"
	}
	return templateSourceFlags{
		Home:   fs.String("home", "", homeUsage),
		Remote: fs.String("remote", "", remoteUsage),
		Branch: fs.String("branch", "", branchUsage),
	}
}
