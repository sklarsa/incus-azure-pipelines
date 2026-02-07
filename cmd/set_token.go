package cmd

import (
	"fmt"
	"os"

	"github.com/sklarsa/incus-azure-pipelines/pool"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(setTokenCmd)
}

var setTokenCmd = &cobra.Command{
	Use:   "set-token <pool-name>",
	Short: "Store a PAT in the system keyring for a pool",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		poolName := args[0]

		fmt.Fprint(os.Stderr, "Enter PAT: ")
		pat, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("failed to read PAT: %w", err)
		}

		if len(pat) == 0 {
			return fmt.Errorf("PAT cannot be empty")
		}

		if err := keyring.Set(pool.KeyringService, poolName, string(pat)); err != nil {
			return fmt.Errorf("failed to store PAT in keyring: %w", err)
		}

		fmt.Fprintf(os.Stderr, "PAT stored in keyring for pool %q\n", poolName)
		return nil
	},
}
