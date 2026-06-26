package generator

import "os"

// GenerateServiceScaffold 是配置驱动的脚手架入口，按 IR、renderer、filesystem sink 三层编排生成流程。
func GenerateServiceScaffold(opts ServiceScaffoldOptions) error {
	ir, err := buildServiceScaffoldIR(opts)
	if err != nil {
		return err
	}
	if err := cleanupLegacyServiceFilesForProfile(ir.Dir, ir.Profile); err != nil {
		return err
	}

	rendered := serviceScaffoldRenderer{}.Render(ir)
	sink := serviceFilesystemSink{Dir: ir.Dir, Stderr: os.Stderr}
	if err := sink.WriteRendered(rendered); err != nil {
		return err
	}
	if err := sink.RunPlugins(ir); err != nil {
		return err
	}

	return nil
}
