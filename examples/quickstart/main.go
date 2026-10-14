// Command quickstart runs one turn on Claude Code or Codex through the
// cloudagent API and prints every message as it arrives.
//
//	go run ./examples/quickstart -harness claudecode "List the files in this directory"
//	go run ./examples/quickstart -harness codex -workdir /path/to/repo "Explain main.go"
//
// The harness CLI (claude or codex) must be on PATH and already logged in.
// Press Ctrl-C to stop the turn.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	cloudagent "github.com/wenfengwang/cloud-agent-sdk/go"
)

func main() {
	harness := flag.String("harness", "claudecode", `harness to drive: "claudecode" or "codex"`)
	workDir := flag.String("workdir", ".", "working directory of the agent")
	model := flag.String("model", "", "model to use; empty selects the harness default")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: quickstart [flags] <prompt>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	prompt := strings.Join(flag.Args(), " ")
	if prompt == "" {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(cloudagent.Harness(*harness), *workDir, *model, prompt); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(harness cloudagent.Harness, workDir, model, prompt string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	agent, err := cloudagent.New(harness, cloudagent.Options{Model: model, WorkDir: workDir})
	if err != nil {
		return err
	}
	defer agent.Close()

	turn, err := agent.Turn(ctx, prompt, cloudagent.TurnOptions{})
	if err != nil {
		return err
	}

	for msg, err := range turn.Messages() {
		if err != nil {
			return err
		}
		printMessage(msg)
	}

	result, err := turn.Result()
	if errors.Is(err, context.Canceled) {
		fmt.Println("\n(turn stopped)")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("\n--- %s: %d input, %d output, %d cache-read tokens\n",
		result.TurnID, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens)
	return nil
}

func printMessage(msg cloudagent.Message) {
	indent := ""
	if msg.ParentToolUseID != "" {
		indent = "    " // produced inside a subagent
	}
	switch msg.Kind {
	case cloudagent.KindUser:
		fmt.Printf("%s> %s\n", indent, msg.User.Text)
	case cloudagent.KindAssistant:
		fmt.Printf("%s%s\n", indent, msg.Assistant.Text)
	case cloudagent.KindReasoning:
		if msg.Reasoning.Text != "" {
			fmt.Printf("%s(thinking) %s\n", indent, msg.Reasoning.Text)
		}
	case cloudagent.KindToolCall:
		fmt.Printf("%s[%s] %s\n", indent, msg.ToolCall.Name, describeCall(msg.ToolCall.Input))
	case cloudagent.KindToolResult:
		fmt.Printf("%s  => %s\n", indent, describeResult(msg.ToolResult))
	}
}

func describeCall(input cloudagent.ToolInput) string {
	switch in := input.(type) {
	case *cloudagent.ShellInput:
		return "$ " + in.Command
	case *cloudagent.FileReadInput:
		return in.Path
	case *cloudagent.FileWriteInput:
		return in.Path
	case *cloudagent.FileEditInput:
		return in.Path
	case *cloudagent.FilePatchInput:
		paths := make([]string, len(in.Changes))
		for i, change := range in.Changes {
			paths[i] = change.Kind + " " + change.Path
		}
		return strings.Join(paths, ", ")
	case *cloudagent.GlobInput:
		return in.Pattern
	case *cloudagent.GrepInput:
		return in.Pattern
	case *cloudagent.WebFetchInput:
		return in.URL
	case *cloudagent.WebSearchInput:
		return in.Query
	case *cloudagent.MCPInput:
		return in.Server + "/" + in.Tool
	case *cloudagent.SubagentInput:
		return in.Description
	default:
		return ""
	}
}

func describeResult(result *cloudagent.ToolResult) string {
	status := "ok"
	if result.IsError {
		status = "error"
	}
	if shell, ok := result.Output.(*cloudagent.ShellOutput); ok && shell.ExitCode != nil {
		status = fmt.Sprintf("%s (exit %d)", status, *shell.ExitCode)
	}
	return status
}
