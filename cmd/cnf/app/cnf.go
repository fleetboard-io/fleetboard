package app

import (
	"context"
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/rest"
	cliflag "k8s.io/component-base/cli/flag"
	logsapi "k8s.io/component-base/logs/api/v1"
	"k8s.io/component-base/term"
	"k8s.io/component-base/version/verflag"
	"k8s.io/klog/v2"

	"github.com/fleetboard-io/fleetboard/pkg/cnf"
	"github.com/fleetboard-io/fleetboard/pkg/tunnel"
	"github.com/spf13/cobra"
)

func init() {
	utilruntime.Must(logsapi.AddFeatureGates(utilfeature.DefaultMutableFeatureGate))
}

// NewCNFCommand creates a *cobra.Command object with default parameters
func NewCNFCommand(ctx context.Context) *cobra.Command {
	o := tunnel.NewOptions()
	cmd := &cobra.Command{
		Use:  "cnf",
		Long: `cnf use dedinic as local ip manager and tunnel manager`,
		// stop printing usage when the command errors
		SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			// silence client-go warnings.
			rest.SetDefaultWarningHandler(rest.NoWarnings{})
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			verflag.PrintAndExitIfRequested()
			fs := cmd.Flags()

			// Activate logging as soon as possible, after that
			// show flags with the final logging configuration.
			if err := logsapi.ValidateAndApply(o.Logs, utilfeature.DefaultFeatureGate); err != nil {
				return err
			}
			cliflag.PrintFlags(fs)

			// set default options
			err := o.Complete()
			if err != nil {
				return err
			}

			// validate options
			if errs := o.Validate(); len(errs) != 0 {
				return utilerrors.NewAggregate(errs)
			}
			cm, err := cnf.NewCNFManager(o)
			if err != nil {
				return err
			}
			if err = cm.Run(ctx); err != nil {
				klog.Error(err)
				return err
			}
			return nil
		},
		Args: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				if len(arg) > 0 {
					return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
				}
			}
			return nil
		},
	}

	fs := cmd.Flags()
	namedFlagSets := o.Flags()
	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}

	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	cliflag.SetUsageAndHelpFunc(cmd, namedFlagSets, cols)

	return cmd
}
