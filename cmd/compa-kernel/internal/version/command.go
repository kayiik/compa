package version

import (
	"github.com/spf13/cobra"

	"github.com/kayiik/compa/cmd/compa-kernel/internal"
	"github.com/kayiik/compa/cmd/compa-kernel/internal/cliui"
	"github.com/kayiik/compa/pkg/config"
)

func NewVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Show version information",
		Run: func(_ *cobra.Command, _ []string) {
			printVersion()
		},
	}

	return cmd
}

func printVersion() {
	build, goVer := config.FormatBuildInfo()
	cliui.PrintVersion(internal.Logo, "compa-kernel "+config.FormatVersion(), build, goVer)
}
