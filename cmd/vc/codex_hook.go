package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// `vc codex-hook` is what the managed Codex config runs on SessionStart,
// UserPromptSubmit and Stop (codexruntime.HookCommand). In the desktop it is
// the Codex half of the chat status channel the Pi extension writes
// (registerDesktopLifecycle in pi_extension.go), plus session.json, which tells
// the desktop the Codex session id to resume the chat with. In a terminal it
// does nothing.
//
// Codex runs it on every turn and feeds a hook's stdout to the model, so it
// never prints to stdout and never fails: every problem is one line on stderr
// and exit code 0.
var codexHookCmd = &cobra.Command{
	Use:                "codex-hook",
	Short:              "Report Codex lifecycle events to the desktop app",
	Hidden:             true,
	DisableFlagParsing: true,
	SilenceUsage:       true,
	SilenceErrors:      true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := runCodexHook(cmd.InOrStdin(), os.Getenv, time.Now); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "vc codex-hook: "+err.Error())
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(codexHookCmd) }

// desktopUUIDPattern is the id shape the desktop status channel accepts
// (status-channel.ts lifecycleEvent); Codex issues its session ids as UUIDv7,
// which fit it too.
var desktopUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// codexHookInputLimit bounds what is read from Codex: a Stop payload carries
// the last assistant message, which is large but not unbounded.
const codexHookInputLimit = 64 << 20

type desktopStatusChannel struct {
	statusPath string
	chatID     string
	generation int64
}

// desktopChannelFromEnv reads the channel the desktop gave this chat. ok is
// false with a nil error outside the desktop.
func desktopChannelFromEnv(getenv func(string) string) (desktopStatusChannel, bool, error) {
	statusPath := getenv("VC_DESKTOP_STATUS_PATH")
	if statusPath == "" {
		return desktopStatusChannel{}, false, nil
	}
	if !filepath.IsAbs(statusPath) {
		return desktopStatusChannel{}, false, errors.New("VC_DESKTOP_STATUS_PATH is not absolute")
	}
	chatID := getenv("VC_DESKTOP_CHAT_ID")
	if !desktopUUIDPattern.MatchString(chatID) {
		return desktopStatusChannel{}, false, errors.New("VC_DESKTOP_CHAT_ID is not a chat id")
	}
	generation, err := strconv.ParseInt(getenv("VC_DESKTOP_STATUS_GENERATION"), 10, 64)
	if err != nil || generation < 1 || generation > maxSafeInteger {
		return desktopStatusChannel{}, false, errors.New("VC_DESKTOP_STATUS_GENERATION is not a positive integer")
	}
	return desktopStatusChannel{statusPath: statusPath, chatID: chatID, generation: generation}, true, nil
}

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER: the desktop reads
// generation and sequence with Number.isSafeInteger.
const maxSafeInteger = 1<<53 - 1

func runCodexHook(stdin io.Reader, getenv func(string) string, now func() time.Time) error {
	input := io.LimitReader(stdin, codexHookInputLimit)
	// Drain whatever is left so Codex never writes into a closed pipe.
	defer func() { _, _ = io.Copy(io.Discard, input) }()

	channel, inDesktop, err := desktopChannelFromEnv(getenv)
	if err != nil {
		return fmt.Errorf("desktop status channel unavailable: %w", err)
	}
	if !inDesktop {
		return nil
	}
	var event struct {
		HookEventName string `json:"hook_event_name"`
		SessionID     string `json:"session_id"`
	}
	if err := json.NewDecoder(input).Decode(&event); err != nil {
		return fmt.Errorf("unreadable hook input: %w", err)
	}
	switch event.HookEventName {
	case "":
		return errors.New("hook input names no hook_event_name")
	case "UserPromptSubmit":
		return channel.writeStatus("Working", now())
	case "Stop":
		return channel.writeStatus("Ready", now())
	case "SessionStart":
		if !desktopUUIDPattern.MatchString(event.SessionID) {
			return errors.New("SessionStart carries no usable session_id")
		}
		return channel.writeSession(event.SessionID)
	default:
		return nil
	}
}

// writeStatus writes the status-channel message the Pi extension writes. Each
// hook is a new process, so the sequence comes from the clock — microseconds,
// which stay far below 2^53 — and never goes below the last one written, so a
// clock stepping back cannot make the desktop drop the message.
func (c desktopStatusChannel) writeStatus(state string, at time.Time) error {
	sequence := at.UnixMicro()
	if last := c.lastSequence(); last >= sequence {
		sequence = last + 1
	}
	if sequence < 1 || sequence > maxSafeInteger {
		return fmt.Errorf("status sequence %d out of range", sequence)
	}
	message := struct {
		Version    int    `json:"version"`
		ChatID     string `json:"chatId"`
		Generation int64  `json:"generation"`
		Sequence   int64  `json:"sequence"`
		State      string `json:"state"`
		Timestamp  string `json:"timestamp"`
	}{1, c.chatID, c.generation, sequence, state, at.UTC().Format("2006-01-02T15:04:05.000Z07:00")}
	return writeJSONAtomically(c.statusPath, message)
}

// lastSequence is the sequence already in status.json for this chat and
// generation, or 0.
func (c desktopStatusChannel) lastSequence() int64 {
	data, err := os.ReadFile(c.statusPath)
	if err != nil {
		return 0
	}
	var previous struct {
		ChatID     string `json:"chatId"`
		Generation int64  `json:"generation"`
		Sequence   int64  `json:"sequence"`
	}
	if json.Unmarshal(data, &previous) != nil || previous.ChatID != c.chatID || previous.Generation != c.generation {
		return 0
	}
	return previous.Sequence
}

// writeSession records the Codex session id beside status.json, for the
// desktop to resume the chat with.
func (c desktopStatusChannel) writeSession(sessionID string) error {
	message := struct {
		Version   int    `json:"version"`
		ChatID    string `json:"chatId"`
		Runtime   string `json:"runtime"`
		SessionID string `json:"sessionId"`
	}{1, c.chatID, "codex", sessionID}
	return writeJSONAtomically(filepath.Join(filepath.Dir(c.statusPath), "session.json"), message)
}

// writeJSONAtomically writes value to path through a temporary file in the
// same folder and a rename, so the desktop never reads half a message.
func writeJSONAtomically(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	_, writeErr := tmp.Write(append(data, '\n'))
	if closeErr := tmp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = renameReplacing(tmp.Name(), path)
	}
	if writeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write %s: %w", filepath.Base(path), writeErr)
	}
	return nil
}

// renameReplacing retries briefly on Windows, where a reader holding the
// target open makes the replace fail for a moment.
func renameReplacing(from, to string) error {
	err := os.Rename(from, to)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 10; i++ {
		time.Sleep(20 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}
