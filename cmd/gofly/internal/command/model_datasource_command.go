package command

import (
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

var runModelDatasource = generator.GenerateModelFromDatasource

func modelMySQLCommand(args []string) error {
	if len(args) == 0 || args[0] == "ddl" {
		if len(args) > 0 {
			args = args[1:]
		}
		return modelGenCommand(args)
	}
	if args[0] == "datasource" {
		return modelMySQLDatasourceCommand(args[1:])
	}
	return fmt.Errorf("%w: expected `gofly model mysql ddl` or `gofly model mysql datasource`", errUsage)
}

func modelPostgresCommand(args []string) error {
	if len(args) == 0 || args[0] == "ddl" {
		if len(args) > 0 {
			args = args[1:]
		}
		return modelGenCommand(args)
	}
	if args[0] == "datasource" {
		return modelPostgresDatasourceCommand(args[1:])
	}
	return fmt.Errorf("%w: expected `gofly model pg ddl` or `gofly model pg datasource`", errUsage)
}
