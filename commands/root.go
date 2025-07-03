package commands

import (
	"fmt"

	"github.com/docker/cli/cli-plugins/plugin"
	"github.com/docker/cli/cli/command"
	"github.com/docker/cli/cli/flags"
	"github.com/docker/model-cli/commands/otel"
	"github.com/docker/model-cli/desktop"
	"github.com/spf13/cobra"
)

// dockerCLI is the Docker CLI environment associated with the command.
var dockerCLI *command.DockerCli

// getDockerCLI is an accessor for dockerCLI that can be passed to other
// packages.
func getDockerCLI() *command.DockerCli {
	return dockerCLI
}

// modelRunner is the model runner context. It is initialized by the root
// command's PersistentPreRunE.
var modelRunner *desktop.ModelRunnerContext

// getModelRunner is an accessor for modelRunner that can be passed to other
// packages.
func getModelRunner() *desktop.ModelRunnerContext {
	return modelRunner
}

// desktopClient is the model runner client. It is initialized by the root
// command's PersistentPreRunE.
var desktopClient *desktop.Client

// getDesktopClient is an accessor for desktopClient that can be passed to other
// packages.
func getDesktopClient() *desktop.Client {
	return desktopClient
}

func NewRootCmd(cli *command.DockerCli) *cobra.Command {
	// If we're running in standalone mode, then we're responsible for
	// initializing the CLI. In this case, we'll need to initialize the client
	// options as well, which we'll add as global flags on the root command. We
	// perform that initialization below so that we can register flags with the
	// root command.
	var globalOptions *flags.ClientOptions

	// Set up the root command.
	var rootCmd *cobra.Command
	rootCmd = &cobra.Command{
		Use:   "model",
		Short: "Docker Model Runner",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Finalize initialization of the CLI.
			if plugin.RunningStandalone() {
				globalOptions.SetDefaultOptions(rootCmd.Flags())
				if err := cli.Initialize(globalOptions); err != nil {
					return fmt.Errorf("unable to configure CLI: %w", err)
				}
			} else if err := plugin.PersistentPreRunE(cmd, args); err != nil {
				return err
			}
			dockerCLI = cli

			// Detect the model runner context and create a client for it.
			var err error
			modelRunner, err = desktop.DetectContext(cmd.Context(), dockerCLI)
			if err != nil {
				return fmt.Errorf("unable to detect model runner context: %w", err)
			}
			desktopClient = desktop.New(modelRunner)

			ctx := otel.SetupTracing(cmd.Context())
			cmd.SetContext(ctx)

			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			otel.CleanupTracing(cmd.Context())
		},
		// If running standalone, then we'll register global Docker flags as
		// top-level flags on the root command, so we'll have to set
		// TraverseChildren in order for those flags to be inherited. We could
		// instead register them as PersistentFlags, but our approach here
		// better matches the behavior of the Docker CLI, where these flags
		// affect all commands, but don't show up in the help output of all
		// commands a "Global Flags".
		TraverseChildren: plugin.RunningStandalone(),
	}

	markCommandExperimental(rootCmd)

	// Initialize client options and register their flags if running in
	// standalone mode.
	if plugin.RunningStandalone() {
		globalOptions = flags.NewClientOptions()
		globalOptions.InstallFlags(rootCmd.Flags())
	}

	// Add subcommands (with tracing).
	rootCmd.AddCommand(
		otel.WrapCommandWithSpan(newVersionCmd()),
		otel.WrapCommandWithSpan(newStatusCmd()),
		otel.WrapCommandWithSpan(newPullCmd()),
		otel.WrapCommandWithSpan(newPushCmd()),
		otel.WrapCommandWithSpan(newPackagedCmd()),
		otel.WrapCommandWithSpan(newListCmd()),
		otel.WrapCommandWithSpan(newLogsCmd()),
		otel.WrapCommandWithSpan(newRunCmd()),
		otel.WrapCommandWithSpan(newRemoveCmd()),
		otel.WrapCommandWithSpan(newInspectCmd()),
		otel.WrapCommandWithSpan(newComposeCmd()),
		otel.WrapCommandWithSpan(newTagCmd()),
		otel.WrapCommandWithSpan(newInstallRunner()),
		otel.WrapCommandWithSpan(newUninstallRunner()),
		otel.WrapCommandWithSpan(newConfigureCmd()),
		otel.WrapCommandWithSpan(newPSCmd()),
		otel.WrapCommandWithSpan(newDFCmd()),
		otel.WrapCommandWithSpan(newUnloadCmd()),
	)
	return rootCmd
}

const annotationExperimentalCLI = "experimentalCLI"

func markCommandExperimental(c *cobra.Command) {
	if _, ok := c.Annotations[annotationExperimentalCLI]; ok {
		return
	}
	if c.Annotations == nil {
		c.Annotations = make(map[string]string)
	}
	c.Annotations[annotationExperimentalCLI] = ""
	c.Short += " (EXPERIMENTAL)"
}
