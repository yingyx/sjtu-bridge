package cli

import (
	"io"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func addPanWriteCommands(root *cobra.Command, connection *string, input io.Reader, write func(any) error) {
	for _, id := range contracts.PanWriteIDs() {
		metadata, _ := contracts.PersonalMetadata(id)
		schema := contracts.PanWriteInputSchema(id)
		parts := contracts.CLIPath(id)
		group := ensureCommandPath(root, parts[:len(parts)-1], metadata.CategoryLabel)
		var q contracts.PanWriteQuery
		var options *jsonInputOptions
		flags := []string{"account", "resource", "view", "limit", "path"}
		command := &cobra.Command{Use: parts[len(parts)-1], Short: metadata.Title, Long: metadata.Description, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if e := requirePrivateConnection(*connection); e != nil {
				return e
			}
			raw, e := options.read(cmd, input, q, flags, schema)
			if e != nil {
				return e
			}
			c, e := client.Open(*connection)
			if e != nil {
				return e
			}
			defer c.Close()
			result, e := c.Invoke(cmd.Context(), contracts.Invocation{Capability: id, Input: raw})
			if e != nil {
				return e
			}
			return write(result)
		}}
		command.Flags().StringVar(&q.AccountRef, "account", "", "")
		command.Flags().StringVar(&q.ResourceRef, "resource", "", "")
		command.Flags().StringVar(&q.View, "view", "created_v1", "")
		command.Flags().IntVar(&q.Limit, "limit", 1, "")
		command.Flags().StringVar(&q.Path, "path", "", "")
		command.Example = "  sjtu capabilities describe " + id + "\n  " + metadata.CLICommand + " --input-json-stdin"
		applySchemaFlagHelp(command, schema)
		options = addJSONInputFlags(command)
		group.AddCommand(command)
	}
}
