package cmd

import (
	"github.com/go-playground/validator/v10"
	"github.com/sklarsa/incus-azure-pipelines/provision"
	"github.com/spf13/cobra"
)

var (
	provisionConf = &provision.Config{}
)

func init() {
	rootCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVarP(&provisionConf.BaseAlias, "base", "b", "", "base image alias (starting point)")
	provisionCmd.Flags().StringVarP(&provisionConf.TargetAlias, "target", "t", "", "target image alias (name of the newly-built image)")
	provisionCmd.Flags().StringArrayVarP(&provisionConf.Scripts, "scripts", "s", []string{}, "paths to provisioning scripts")
	provisionCmd.Flags().StringVarP(&provisionConf.ProjectName, "project", "p", "", "name of incus project to build the image in")

	_ = provisionCmd.MarkFlagRequired("base")
	_ = provisionCmd.MarkFlagRequired("target")
}

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "provision an image to use for azure CI runners",
	RunE: func(cmd *cobra.Command, args []string) error {

		v := validator.New(validator.WithRequiredStructEnabled())
		if err := v.Struct(provisionConf); err != nil {
			return err
		}

		return provision.BaseImage(ctx, c, *provisionConf)
	},
}
