package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// commandOutput owns stderr. Finite commands show a live region of their stages
// in a terminal and print warnings; run writes JSON logs.
type commandOutput struct {
	activityEnabled     bool
	mu                  sync.Mutex
	out                 io.Writer
	style               textStyle
	logger              *slog.Logger
	progress            *terminalProgress
	interactive, daemon bool
	level               string
	threshold           slog.LevelVar
}

func (o *commandOutput) start(cmd *cobra.Command) error {
	o.out = cmd.ErrOrStderr()
	o.style = newTextStyle(o.out)
	o.daemon = cmd.Name() == "run"
	o.threshold.Set(slog.LevelInfo)
	if o.daemon {
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
		o.threshold.Set(level)
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	noProgress, _ := cmd.Flags().GetBool("no-progress")
	o.activityEnabled = !o.daemon && !jsonOutput && !noProgress && cmd.Name() != "schema"
	o.interactive = o.activityEnabled && terminalOutput(o.out) && os.Getenv("CI") == ""
	handler := slog.NewJSONHandler(o.out, &slog.HandlerOptions{
		Level: &o.threshold,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == "report_detail" {
				return slog.Attr{}
			}
			return attr
		},
	})
	o.logger = slog.New(&activityHandler{Handler: handler, output: o})
	cmd.SetOut(reportWriter{Writer: cmd.OutOrStdout(), output: o})
	return nil
}

// reportWriter clears the live region before the report is written.
type reportWriter struct {
	io.Writer
	output *commandOutput
}

func (w reportWriter) Write(data []byte) (int, error) {
	w.output.endProgress()
	return w.Writer.Write(data)
}

func (o *commandOutput) finish(cmd *cobra.Command, err error) {
	interrupted := cmd.Context() != nil && errors.Is(context.Cause(cmd.Context()), errInterrupted)
	terminated := cmd.Context() != nil && errors.Is(context.Cause(cmd.Context()), errTerminated)
	if interrupted {
		err = errInterrupted
	}
	o.endProgress()
	if o.logger == nil && err == nil {
		return
	}
	if cmd.Name() == "run" {
		if o.logger == nil {
			o.logger = slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), nil))
		}
		if err != nil && !interrupted && !terminated {
			o.logger.Error("Command failed", "error", err)
		} else {
			o.logger.Info("Service stopped")
		}
		return
	}
	if err != nil {
		style := newTextStyle(cmd.ErrOrStderr())
		label, message := style.paint("✗", color.FgHiRed), err.Error()
		if interrupted {
			message, label = "Interrupted.", style.paint("–", color.FgHiYellow)
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", label, reportText(strings.Join(strings.Fields(message), " ")))
	}
}

// activityHandler sends stage records to the live region and prints warnings
// the report does not already carry.
type activityHandler struct {
	slog.Handler
	output *commandOutput
	attrs  []slog.Attr
}

func (h *activityHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &activityHandler{Handler: h.Handler.WithAttrs(attrs), output: h.output, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *activityHandler) WithGroup(name string) slog.Handler {
	return &activityHandler{Handler: h.Handler.WithGroup(name), output: h.output, attrs: h.attrs}
}

func (h *activityHandler) Handle(ctx context.Context, record slog.Record) error {
	a := readActivity(record, h.attrs)
	o := h.output
	if o.daemon {
		if a.stage || a.progress || a.status {
			record.Level = slog.LevelDebug
		}
		if !h.Enabled(ctx, record.Level) {
			return nil
		}
		return h.Handler.Handle(ctx, record)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if a.stage || a.progress || a.status {
		if o.activityEnabled || o.interactive {
			if o.progress == nil {
				o.progress = newTerminalProgress(o.out)
				if !o.interactive {
					o.progress.startPlain()
				}
			}
			o.progress.update(a)
		}
		return nil
	}
	if record.Level < slog.LevelWarn {
		return nil
	}
	var detail bool
	var attrs []string
	read := func(attr slog.Attr) bool {
		if attr.Key == "report_detail" {
			detail = attr.Value.Bool()
			return true
		}
		attrs = append(attrs, attr.Key+"="+attr.Value.String())
		return true
	}
	for _, attr := range h.attrs {
		read(attr)
	}
	record.Attrs(read)
	if detail {
		return nil
	}
	message := record.Message
	if len(attrs) > 0 {
		message += "; " + strings.Join(attrs, "; ")
	}
	line := o.style.paint("!", color.FgHiYellow) + " " + reportText(message) + "\n"
	if o.progress != nil {
		return o.progress.write(o.out, line)
	}
	_, err := io.WriteString(o.out, line)
	return err
}

func (o *commandOutput) endProgress() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.interactive = false
	o.activityEnabled = false
	if o.progress != nil {
		o.progress.stop()
		o.progress = nil
	}
}
