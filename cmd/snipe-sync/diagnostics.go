package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"
)

type commandOutput struct {
	mu                                sync.Mutex
	out                               io.Writer
	logger                            *slog.Logger
	progress                          *terminalProgress
	interactive                       bool
	reportWritten                     bool
	started                           time.Time
	staged                            atomic.Bool
	level, format                     string
	threshold                         slog.LevelVar
	explicitLevel, daemon             bool
	quiet, verbose, debug, noProgress bool
}

func newCommandOutput(cmd *cobra.Command) *commandOutput {
	o := &commandOutput{}
	flags := cmd.PersistentFlags()
	flags.StringVar(&o.level, "log-level", "info", "Log level: debug, info, warn or error")
	flags.StringVar(&o.format, "log-format", "text", "Stderr log format: text or json (run defaults to json)")
	flags.BoolVarP(&o.quiet, "quiet", "q", false, "Show only warnings and errors on stderr")
	flags.BoolVarP(&o.verbose, "verbose", "v", false, "Show debug diagnostics")
	flags.BoolVarP(&o.debug, "debug", "d", false, "Show debug diagnostics (same as --verbose)")
	flags.BoolVar(&o.noProgress, "no-progress", false, "Use ordinary log lines without live terminal progress")
	cmd.MarkFlagsMutuallyExclusive("log-level", "quiet", "verbose", "debug")
	return o
}

func (o *commandOutput) start(cmd *cobra.Command) error {
	o.out = cmd.ErrOrStderr()
	o.daemon = cmd.Name() == "run"
	if o.daemon && !cmd.Flags().Changed("log-format") {
		o.format = "json"
	}
	o.explicitLevel = cmd.Flags().Changed("log-level") || cmd.Flags().Changed("quiet") || cmd.Flags().Changed("verbose") || cmd.Flags().Changed("debug")
	var level slog.Level
	switch strings.ToLower(o.level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return fmt.Errorf("invalid log level %q: use debug, info, warn or error", o.level)
	}
	if o.quiet {
		level = slog.LevelWarn
	}
	if o.verbose || o.debug {
		level = slog.LevelDebug
	}
	if o.format != "text" && o.format != "json" {
		return fmt.Errorf("invalid log format %q: use text or json", o.format)
	}
	terminal := terminalOutput(o.out)
	o.interactive = terminal && !o.daemon && !o.noProgress && o.format == "text" && os.Getenv("CI") == ""
	o.threshold.Set(level)
	var handler slog.Handler
	replace := func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == "stage" || attr.Key == "progress" || attr.Key == "progress_final" || attr.Key == "stage_result" {
			return slog.Attr{}
		}
		return attr
	}
	if o.format == "json" {
		handler = slog.NewJSONHandler(o, &slog.HandlerOptions{Level: &o.threshold})
	} else {
		handler = tint.NewTextHandler(o, &tint.Options{Level: &o.threshold, NoColor: !newTextStyle(o.out).enabled, TimeFormat: "15:04:05", ReplaceAttr: replace})
	}
	o.logger = slog.New(&stageHandler{Handler: handler, output: o})
	o.started = time.Now()
	cmd.SetOut(reportWriter{Writer: cmd.OutOrStdout(), output: o})
	return nil
}

func (o *commandOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.progress != nil {
		return o.progress.Write(data)
	}
	return o.out.Write(data)
}

func (o *commandOutput) stop() {
	o.endProgress(nil)
}

func (o *commandOutput) finish(cmd *cobra.Command, err error) {
	o.endProgress(err)
	o.interactive = false
	if o.logger == nil {
		if o.format == "json" || (cmd.Name() == "run" && !cmd.Flags().Changed("log-format")) {
			o.logger = slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), nil))
		} else {
			o.logger = slog.New(tint.NewTextHandler(cmd.ErrOrStderr(), &tint.Options{NoColor: true}))
		}
	}
	if format, _ := cmd.Flags().GetString("output"); err != nil && format == "json" && !o.reportWritten {
		_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"error": err.Error()})
	}
	switch {
	case errors.Is(err, context.Canceled):
		o.logger.Warn("Interrupted")
	case err != nil:
		o.logger.Error("Command failed", "error", err)
	case o.daemon:
		o.logger.Info("Service stopped")
	case o.staged.Load() && !o.reportWritten:
		o.logger.Info("Completed", "elapsed", time.Since(o.started).Round(time.Millisecond))
	}
}

// Stop live rendering before stdout reports so terminal redraws cannot erase them.
type reportWriter struct {
	io.Writer
	output *commandOutput
}

func (w reportWriter) Write(data []byte) (int, error) {
	w.output.stop()
	w.output.reportWritten = true
	return w.Writer.Write(data)
}

type stageHandler struct {
	slog.Handler
	output *commandOutput
	attrs  []slog.Attr
}

func (h *stageHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &stageHandler{Handler: h.Handler.WithAttrs(attrs), output: h.output, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *stageHandler) WithGroup(name string) slog.Handler {
	return &stageHandler{Handler: h.Handler.WithGroup(name), output: h.output, attrs: h.attrs}
}

func (h *stageHandler) Handle(ctx context.Context, record slog.Record) error {
	a := readActivity(record, h.attrs)
	o := h.output
	if (a.stage || a.progress || a.status) && o.daemon {
		record.Level = slog.LevelDebug
	}
	if !h.Enabled(ctx, record.Level) {
		return nil
	}
	if a.stage {
		o.staged.Store(true)
	}
	if o.interactive && (a.stage || a.progress || a.status) {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.progress == nil {
			o.progress = newTerminalProgress(o.out)
		}
		o.progress.update(a)
		return nil
	}
	if a.progress && !a.final {
		record.Level = slog.LevelDebug
		if !h.Enabled(ctx, record.Level) {
			return nil
		}
	}
	return h.Handler.Handle(ctx, record)
}

func (o *commandOutput) endProgress(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.progress != nil {
		outcome := ""
		if err != nil {
			outcome = "failed"
		}
		if errors.Is(err, context.Canceled) {
			outcome = "interrupted"
		}
		o.progress.stop(outcome)
		o.progress = nil
	}
}
