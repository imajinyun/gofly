package command

import "flag"

type templateSourceFlags struct {
	Home   *string
	Remote *string
	Branch *string
}

type templateDirectorySourceFlags struct {
	Dir    *string
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

func registerTemplateDirectorySourceFlags(fs *flag.FlagSet) templateDirectorySourceFlags {
	return templateDirectorySourceFlags{
		Dir:    fs.String("dir", "", "template output directory"),
		Home:   fs.String("home", "", "template output directory"),
		Remote: fs.String("remote", "", "remote template repository or local directory"),
		Branch: fs.String("branch", "", "remote template branch"),
	}
}

func (f templateDirectorySourceFlags) normalize() {
	if valueFromStringFlag(f.Dir) == "" {
		setStringFlag(f.Dir, valueFromStringFlag(f.Home))
	}
}
