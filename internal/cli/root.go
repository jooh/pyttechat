package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"example.com/llm-chat-web/internal/buildinfo"
	"example.com/llm-chat-web/internal/chat"
	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/llm/dummy"
	"example.com/llm-chat-web/internal/llm/openresponses"
	"example.com/llm-chat-web/internal/web"

	"github.com/spf13/cobra"
)

type rootOptions struct {
	proxyURL        string
	proxyToken      string
	model           string
	reasoningEffort string
	proxyTimeout    time.Duration
	webAddr         string
	secureCookies   bool
}

type webServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
}

var (
	listenTCP = func(ctx context.Context, addr string) (net.Listener, error) {
		var listenConfig net.ListenConfig
		return listenConfig.Listen(ctx, "tcp", addr)
	}
	newWebServer = func(addr string, handler http.Handler) webServer {
		return &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
	commandHelp = func(cmd *cobra.Command) error {
		return cmd.Help()
	}
	printUsage = printUsageToError
)

func NewRootCommand(stdin io.Reader, stdout, stderr io.Writer) *cobra.Command {
	opts := rootOptions{
		proxyURL:      os.Getenv("PYTTECHAT_LLM_PROXY_URL"),
		proxyToken:    os.Getenv("PYTTECHAT_LLM_PROXY_TOKEN"),
		model:         os.Getenv("PYTTECHAT_MODEL"),
		proxyTimeout:  openresponses.DefaultTimeout,
		webAddr:       envString("PYTTECHAT_WEB_ADDR", ":3000"),
		secureCookies: envBool("PYTTECHAT_SECURE_COOKIES"),
	}
	if value := os.Getenv("PYTTECHAT_LLM_PROXY_TIMEOUT"); value != "" {
		if timeout, err := time.ParseDuration(value); err == nil && timeout > 0 {
			opts.proxyTimeout = timeout
		}
	}

	rootCmd := &cobra.Command{
		Use:           "pyttechat",
		Short:         "Minimal LLM chat backend CLI",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.PersistentFlags().StringVar(&opts.proxyURL, "proxy-url", opts.proxyURL, "OpenResponses-compatible LLM proxy base URL")
	rootCmd.PersistentFlags().StringVarP(&opts.model, "model", "m", opts.model, "model name to forward to the LLM proxy")
	rootCmd.PersistentFlags().StringVar(&opts.reasoningEffort, "reasoning-effort", opts.reasoningEffort, "reasoning effort to forward to the LLM proxy")
	rootCmd.PersistentFlags().DurationVar(&opts.proxyTimeout, "proxy-timeout", opts.proxyTimeout, "LLM proxy request timeout")

	rootCmd.AddCommand(newAskCommand(stdout, stderr, &opts))
	rootCmd.AddCommand(newChatCommand(stdin, stdout, stderr, &opts))
	rootCmd.AddCommand(newServeCommand(stdout, stderr, &opts))
	rootCmd.AddCommand(newVersionCommand(stdout))

	return rootCmd
}

func newServeCommand(stdout, stderr io.Writer, opts *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "serve",
		Short: "Start the web chat server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			handler := web.NewServer(web.Options{
				Client:          newLLMClient(*opts),
				Model:           opts.model,
				ReasoningEffort: opts.reasoningEffort,
				CookieSecure:    opts.secureCookies,
			})
			server := newWebServer(opts.webAddr, handler)
			listener, err := listenTCP(ctx, opts.webAddr)
			if err != nil {
				return err
			}
			defer listener.Close()

			errc := make(chan error, 1)
			go func() {
				errc <- server.Serve(listener)
			}()

			fmt.Fprintf(stderr, "pyttechat web listening on %s\n", listener.Addr().String())
			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(shutdownCtx); err != nil {
					return err
				}
				return nil
			case err := <-errc:
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			}
		},
	}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.Flags().StringVar(&opts.webAddr, "addr", opts.webAddr, "HTTP listen address")
	command.Flags().BoolVar(&opts.secureCookies, "secure-cookies", opts.secureCookies, "set the Secure attribute on browser session cookies")
	return command
}

func newAskCommand(stdout, stderr io.Writer, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "ask PROMPT",
		Short: "Send a prompt to the configured LLM",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if err := printUsage(cmd); err != nil {
					return err
				}
				return fmt.Errorf("prompt is required")
			}

			session := chat.NewService(newLLMClient(*opts)).NewSession()
			stream, err := session.Send(cmd.Context(), strings.Join(args, " "), chat.SendOptions{
				Model:                 opts.model,
				ReasoningEffort:       opts.reasoningEffort,
				RenderingInstructions: chat.WebRenderingInstructions(),
			})
			if err != nil {
				if errors.Is(err, chat.ErrEmptyPrompt) {
					return fmt.Errorf("prompt must not be empty")
				}
				return err
			}

			return printStream(stream, stdout, stderr)
		},
	}
}

func newChatCommand(stdin io.Reader, stdout, stderr io.Writer, opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Start an ephemeral multi-turn chat session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session := chat.NewService(newLLMClient(*opts)).NewSession()
			scanner := bufio.NewScanner(stdin)
			for scanner.Scan() {
				stream, err := session.Send(cmd.Context(), scanner.Text(), chat.SendOptions{
					Model:                 opts.model,
					ReasoningEffort:       opts.reasoningEffort,
					RenderingInstructions: chat.WebRenderingInstructions(),
				})
				if err != nil {
					if errors.Is(err, chat.ErrEmptyPrompt) {
						return fmt.Errorf("prompt must not be empty")
					}
					return err
				}
				if err := printStream(stream, stdout, stderr); err != nil {
					return err
				}
			}
			return scanner.Err()
		},
	}
}

func newLLMClient(opts rootOptions) llm.Client {
	if opts.proxyURL != "" {
		return openresponses.NewClientWithOptions(opts.proxyURL, openresponses.Options{
			Timeout:     opts.proxyTimeout,
			BearerToken: opts.proxyToken,
		})
	}
	return dummy.NewClient()
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func printStream(stream *chat.TurnStream, stdout, stderr io.Writer) error {
	defer stream.Close()

	wroteText := false
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		switch event.Type {
		case llm.EventTextDelta:
			if _, err := fmt.Fprint(stdout, event.Delta); err != nil {
				return err
			}
			if event.Delta != "" {
				wroteText = true
			}
		case llm.EventReasoningDelta:
			if _, err := fmt.Fprint(stderr, event.Delta); err != nil {
				return err
			}
		}
	}
	if wroteText {
		_, err := fmt.Fprintln(stdout)
		return err
	}
	return nil
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

func Execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := NewRootCommand(stdin, stdout, stderr) //nolint:contextcheck
	cmd.SetArgs(args)
	cmd.SetContext(ctx)

	if len(args) == 0 {
		if err := commandHelp(cmd); err != nil {
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
