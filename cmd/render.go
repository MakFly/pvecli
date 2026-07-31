package cmd

import (
	"github.com/MakFly/pvecli/internal/output"
	"github.com/spf13/cobra"
)

// renderOptions reads the presentation flags shared by every listing command.
func renderOptions(cmd *cobra.Command) (output.Options, error) {
	raw, _ := cmd.Flags().GetString("output")
	format, err := output.ParseFormat(raw)
	if err != nil {
		return output.Options{}, &exitError{code: 2, msg: err.Error()}
	}
	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	columns, _ := cmd.Flags().GetStringSlice("columns")

	return output.Options{Format: format, NoHeaders: noHeaders, Columns: columns}, nil
}

// addRenderFlags declares the presentation flags on a listing command.
func addRenderFlags(c *cobra.Command) {
	c.Flags().StringP("output", "o", string(output.Table), "format de sortie : table, json ou yaml")
	c.Flags().Bool("no-headers", false, "masque la ligne d'en-tête (mode table)")
	c.Flags().StringSlice("columns", nil, "colonnes à afficher, dans l'ordre (mode table)")
}
