package inspectcmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mgantlett/nomos/substrate/cli/tui"
	"github.com/mgantlett/nomos/substrate/src/nomos/core/types"
	"github.com/mgantlett/nomos/substrate/src/nomos/modules/session"
	"github.com/spf13/cobra"
)

func newSessionNameCmd() *cobra.Command {
	var sessionID string
	var brainDir string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "name [NAME]",
		Short: "Assign a human-readable title or alias to an agent conversation session",
		Long: `Assign a human-readable title or alias to an agent conversation session.
If no session ID is specified via --id, the currently active session is auto-discovered.`,
		Example: tui.HelpSessionNameExample,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := extractNameFromArgs(args)
			if name == "" {
				return types.NewNomosError(types.ErrCodeValidationFailed, "session title/name is required (e.g. nomos session name \"Mission Refactoring\")")
			}
			targetID, err := resolveSessionTargetID(brainDir, sessionID)
			if err != nil {
				return err
			}
			meta, err := session.SaveSessionName(brainDir, targetID, name)
			if err != nil {
				return types.NewNomosError(types.ErrCodeCore, "failed saving session metadata: %v", err)
			}
			if jsonOutput {
				data, mErr := json.MarshalIndent(meta, "", "  ")
				if mErr != nil {
					return types.NewNomosError(types.ErrCodeCore, "failed marshaling JSON: %v", mErr)
				}
				fmt.Println(string(data))
				return nil
			}
			renderSessionNamedCard(meta)
			return nil
		},
	}

	cmd.Flags().StringVarP(&sessionID, "id", "i", "", "Target conversation session ID (defaults to active session)")
	cmd.Flags().StringVarP(&brainDir, "brain", "b", "", "Session storage or brain directory path override")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in machine-readable JSON format")
	return cmd
}

func resolveSessionTargetID(brainDir, explicitID string) (string, error) {
	if explicitID != "" {
		return explicitID, nil
	}
	discovered, err := session.DiscoverSessions(brainDir, "")
	if err != nil || len(discovered) == 0 {
		return "", types.NewNomosError(types.ErrCodeCore, "no active agent conversation sessions found; specify target via --id")
	}
	return discovered[0].ID, nil
}

func extractNameFromArgs(args []string) string {
	name := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "name=") {
			trimmed := strings.TrimPrefix(arg, "name=")
			trimmed = strings.Trim(trimmed, "\"")
			trimmed = strings.Trim(trimmed, "'")
			name = trimmed
		} else if name == "" {
			name = strings.Trim(arg, "\"'")
		} else {
			name = name + " " + strings.Trim(arg, "\"'")
		}
	}
	return strings.TrimSpace(name)
}
