package command

import (
	"io"
	"os"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func IsCompilerPluginMode() bool {
	switch os.Getenv("GOFLY_PLUGIN_MODE") {
	case "protoc", "protobuf":
		return true
	default:
		return false
	}
}

func ExecuteCompilerPluginMode(in io.Reader, out io.Writer) error {
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	resp, err := generator.MarshalProtocPluginResponse(data, generator.ProtocPluginOptions{
		Module:           os.Getenv("GOFLY_MODULE"),
		NameFromFilename: os.Getenv("GOFLY_NAME_FROM_FILENAME") == "1" || os.Getenv("GOFLY_NAME_FROM_FILENAME") == "true",
		NoClient:         os.Getenv("GOFLY_NO_CLIENT") == "1" || os.Getenv("GOFLY_NO_CLIENT") == "true",
		Multiple:         os.Getenv("GOFLY_MULTIPLE") == "1" || os.Getenv("GOFLY_MULTIPLE") == "true",
	})
	if err != nil {
		return err
	}
	_, err = out.Write(resp)
	return err
}
