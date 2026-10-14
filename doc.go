// Package cloudagent gives Claude Code and Codex one programmable Go API.
//
// New returns an AgentRuntime: one agent backed by one harness, holding one
// linear conversation. AgentRuntime.Turn starts a turn and returns a Turn
// handle whose Messages yields complete, typed canonical messages, whose
// Result blocks for the outcome, and whose Terminate stops the turn
// gracefully. Harnesses run without permission prompts, and conversation data
// lives only in the harness home.
//
//	agent, err := cloudagent.New(cloudagent.Claude, cloudagent.Options{WorkDir: "."})
//	if err != nil {
//		return err
//	}
//	defer agent.Close()
//
//	turn, err := agent.Turn(ctx, "fix the failing test", cloudagent.TurnOptions{})
//	if err != nil {
//		return err
//	}
//	for msg, err := range turn.Messages() {
//		if err != nil {
//			return err
//		}
//		if call := msg.ToolCall; call != nil {
//			if sh, ok := call.Input.(*cloudagent.ShellInput); ok {
//				fmt.Println("$", sh.Command)
//			}
//		}
//	}
//	res, err := turn.Result()
package cloudagent
