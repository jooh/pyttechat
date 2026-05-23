package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"example.com/llm-chat-web/internal/buildinfo"
	"example.com/llm-chat-web/internal/chat"
	"example.com/llm-chat-web/internal/llm/dummy"

	"github.com/spf13/cobra"
)

func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	service := chat.NewService(dummy.NewClient())

	rootCmd := &cobra.Command{
		Use:           "pyttechat",
		Short:         "Minimal LLM chat backend CLI",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)

	rootCmd.AddCommand(newAskCommand(stdout, service))
	rootCmd.AddCommand(newVersionCommand(stdout))

	return rootCmd
}

func newAskCommand(stdout io.Writer, service chat.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "ask PROMPT",
		Short: "Send a prompt to the configured LLM",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if err := printUsageToError(cmd); err != nil {
					return err
				}
				return fmt.Errorf("prompt is required")
			}

			response, err := service.Send(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				if errors.Is(err, chat.ErrEmptyPrompt) {
					return fmt.Errorf("prompt must not be empty")
				}
				return err
			}

			_, err = fmt.Fprintln(stdout, response.Text)
			return err
		},
	}
}

func printUsageToError(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	defer cmd.SetOut(out)

	return cmd.Usage()
}

func newVersionCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(stdout, buildinfo.Summary())
			return err
		},
	}
}

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd := NewRootCommand(stdout, stderr)
	cmd.SetArgs(args)
	cmd.SetContext(ctx)

	if len(args) == 0 {
		if err := cmd.Help(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	return 0
}
