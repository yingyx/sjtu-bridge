package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/yingyx/sjtu-bridge/client"
	"github.com/yingyx/sjtu-bridge/contracts"
)

func addCanvasCommands(root *cobra.Command, connection *string, input io.Reader, write func(any) error) {
	for _, id := range contracts.CanvasCapabilityIDs() {
		r, _ := contracts.CanvasRules(id)
		metadata, _ := contracts.PersonalMetadata(id)
		schema := contracts.CanvasInputSchema(id)
		parts := contracts.CLIPath(id)
		group := ensureCommandPath(root, parts[:len(parts)-1], metadata.CategoryLabel)
		var q contracts.CanvasQuery
		var options *jsonInputOptions
		flags := []string{"account", "view", "limit"}
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
			value, e := c.Invoke(cmd.Context(), contracts.Invocation{Capability: id, Input: raw})
			if e != nil {
				return e
			}
			return write(value)
		}}
		command.Flags().StringVar(&q.AccountRef, "account", "", "")
		command.Flags().StringVar(&q.View, "view", "", "")
		limit := 20
		if !r.List {
			limit = 1
		}
		command.Flags().IntVar(&q.Limit, "limit", limit, "")
		command.Example = "  sjtu capabilities describe " + id + " --connection CONNECTION\n  " + metadata.CLICommand + " --connection CONNECTION --account ACCOUNT --view " + r.View + " --limit " + fmt.Sprint(limit)
		if !r.Directory {
			command.Flags().StringVar(&q.CourseRef, "course", "", "")
			flags = append(flags, "course")
			command.Example += " --course COURSE"
		}
		if r.List {
			command.Flags().StringVar(&q.Cursor, "cursor", "", "")
			flags = append(flags, "cursor")
		}
		if r.Item {
			command.Flags().StringVar(&q.ItemID, "item-id", "", "")
			flags = append(flags, "item-id")
			command.Example += " --item-id ITEM_ID"
		}
		if r.Module {
			command.Flags().StringVar(&q.ModuleID, "module-id", "", "")
			flags = append(flags, "module-id")
			command.Example += " --module-id MODULE_ID"
		}
		if r.Dates {
			command.Flags().StringVar(&q.StartDate, "start-date", "", "")
			command.Flags().StringVar(&q.EndDate, "end-date", "", "")
			flags = append(flags, "start-date", "end-date")
			command.Example += " --start-date YYYY-MM-DD --end-date YYYY-MM-DD"
		}
		applySchemaFlagHelp(command, schema)
		options = addJSONInputFlags(command)
		group.AddCommand(command)
	}
}
