package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// `vc codex-hook` is what Codex runs on SessionStart, UserPromptSubmit and Stop
// (spec 2026-09-29-desktop-codex-chats-design, item 3). It is the Codex half of
// the desktop status channel the Pi extension already writes, plus the one fact
// the desktop needs to reopen a Codex chat: its session id.
//
// Driven as a separate process, the way Codex drives it: JSON on stdin, the
// environment the desktop gives the session, vc's own main() (the test binary
// re-executed, see TestCodexHookHelperProcess). That is the only way to see two
// things a cobra-level test cannot — that `vc codex-hook` gets past main's
// welcome gate on a non-TTY stdin with nobody signed in (the gate otherwise
// exits 1 with "auth failed" before cobra dispatches), and that the exit code
// is 0: a failing hook must never get in the way of Codex's turn.

const hookChatID = "5b0c7a2e-1f3d-4c8e-9a6b-2d4e6f8a0c1e"

// Payloads as Codex 0.158 sends them, captured from a live session 29.09
// (scratchpad hookout/session_start.json, stop.json). The session id is a
// UUIDv7, which is what Codex issues.
const (
	hookSessionID      = "01a0ec63-35c1-7d02-bc6a-c06464a61565"
	sessionStartInput  = `{"session_id":"01a0ec63-35c1-7d02-bc6a-c06464a61565","transcript_path":"/tmp/codex-home/sessions/2026/09/29/rollout-2026-09-29T12-58-39-01a0ec63-35c1-7d02-bc6a-c06464a61565.jsonl","cwd":"/tmp/work","hook_event_name":"SessionStart","model":"gpt-6-sol","permission_mode":"bypassPermissions","source":"startup"}`
	userPromptInput    = `{"session_id":"01a0ec63-35c1-7d02-bc6a-c06464a61565","turn_id":"01a0ec63-35e3-70e2-84e3-d835adb79fb4","transcript_path":"/tmp/codex-home/sessions/2026/09/29/rollout-2026-09-29T12-58-39-01a0ec63-35c1-7d02-bc6a-c06464a61565.jsonl","cwd":"/tmp/work","hook_event_name":"UserPromptSubmit","model":"gpt-6-sol","permission_mode":"bypassPermissions","prompt":"привет"}`
	stopInput          = `{"session_id":"01a0ec63-35c1-7d02-bc6a-c06464a61565","turn_id":"01a0ec63-35e3-70e2-84e3-d835adb79fb4","transcript_path":"/tmp/codex-home/sessions/2026/09/29/rollout-2026-09-29T12-58-39-01a0ec63-35c1-7d02-bc6a-c06464a61565.jsonl","cwd":"/tmp/work","hook_event_name":"Stop","model":"gpt-6-sol","permission_mode":"bypassPermissions","stop_hook_active":false,"last_assistant_message":"да"}`
	preToolUseInput    = `{"session_id":"01a0ec63-35c1-7d02-bc6a-c06464a61565","turn_id":"t","transcript_path":null,"cwd":"/tmp/work","hook_event_name":"PreToolUse","model":"gpt-6-sol","permission_mode":"default","tool_name":"shell","tool_input":{},"tool_use_id":"x"}`
	maxSafeJSONInteger = 1<<53 - 1
)

type hookRun struct {
	exitCode       int
	stdout, stderr string
}

// codexHookHelperEnv makes a re-executed test binary run vc's main() as
// `vc codex-hook` (see TestCodexHookHelperProcess). A built vc would be closer
// still, but `go build` inside this package cannot reach the module cache:
// TestMain points HOME at a sandbox, and GOMODCACHE lives under HOME.
const codexHookHelperEnv = "VC_TEST_CODEX_HOOK_AS_VC"

type hookHarness struct {
	t *testing.T
}

func newHookHarness(t *testing.T) *hookHarness {
	t.Helper()
	return &hookHarness{t: t}
}

// run executes `vc codex-hook` with stdin and exactly the given environment.
// Nothing from the test's own environment leaks in, so a VC_DESKTOP_* in the
// developer's shell cannot decide the case. The child gets its own TMPDIR,
// returned as scratch: TestMain's home sandbox is created there, so anything
// the hook wrote into a home shows up under it.
func (h *hookHarness) run(stdin string, env map[string]string) (hookRun, string) {
	h.t.Helper()
	scratch := h.t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexHookHelperProcess$")
	cmd.Stdin = strings.NewReader(stdin)
	base := []string{
		codexHookHelperEnv + "=1",
		"HOME=" + scratch, "USERPROFILE=" + scratch,
		"TMPDIR=" + scratch, "TEMP=" + scratch, "TMP=" + scratch,
		"PATH=" + os.Getenv("PATH"),
	}
	if runtime.GOOS == "windows" {
		base = append(base, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	for k, v := range env {
		base = append(base, k+"="+v)
	}
	cmd.Env = base
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start vc codex-hook: %v", err)
	}
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		h.t.Fatal("vc codex-hook did not finish in 20s — a hook that hangs stalls Codex's turn")
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		h.t.Fatalf("vc codex-hook: %v", err)
	}
	return hookRun{exitCode: code, stdout: stdout.String(), stderr: stderr.String()}, scratch
}

// TestCodexHookHelperProcess is the child half of TestCodexHookCommand: with
// the helper variable set it becomes `vc codex-hook` — main() itself, welcome
// gate included — and exits with vc's own status. Under plain `go test` it is
// a skip.
func TestCodexHookHelperProcess(t *testing.T) {
	if os.Getenv(codexHookHelperEnv) != "1" {
		t.Skip("child process entry point; runs only when " + codexHookHelperEnv + " is set")
	}
	os.Args = []string{os.Args[0], "codex-hook"}
	main()
	// main returns on success; every failure path exits on its own.
	os.Exit(0)
}

// regularFiles lists every regular file under dir.
func regularFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// channel is a fresh status-channel directory, as the desktop creates per chat.
func (h *hookHarness) channel() (dir, statusPath string) {
	h.t.Helper()
	dir = h.t.TempDir()
	return dir, filepath.Join(dir, "status.json")
}

func first(r hookRun, _ string) hookRun { return r }

func desktopHookEnv(statusPath string) map[string]string {
	return map[string]string{
		"VC_DESKTOP_STATUS_PATH":       statusPath,
		"VC_DESKTOP_CHAT_ID":           hookChatID,
		"VC_DESKTOP_STATUS_GENERATION": "3",
	}
}

func hookDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// wantQuietSuccess: exit 0 and nothing on stdout. Codex feeds a
// UserPromptSubmit/SessionStart hook's stdout to the model as extra context,
// so a stray line from vc would land in the person's conversation.
func wantQuietSuccess(t *testing.T, name string, r hookRun) {
	t.Helper()
	if r.exitCode != 0 {
		t.Fatalf("%s: vc codex-hook exited %d, want 0 — a hook must never fail Codex's turn\nstderr: %s", name, r.exitCode, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("%s: vc codex-hook wrote %q to stdout; Codex hands hook stdout to the model", name, r.stdout)
	}
}

func readJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("%s is not a JSON object: %v\n%s", path, err, data)
	}
	return obj
}

func objectKeys(obj map[string]any) string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// statusMessage checks the desktop status-channel schema (status-channel.ts
// lifecycleEvent) and returns the sequence. The desktop rejects anything but
// exactly these six keys, a safe-integer generation and sequence ≥ 1, and an
// ISO timestamp — and a rejected message shows the chat as "status channel
// schema rejected" instead of Working/Ready.
func statusMessage(t *testing.T, path, wantState string) int64 {
	t.Helper()
	obj := readJSONObject(t, path)
	if got := objectKeys(obj); got != "chatId,generation,sequence,state,timestamp,version" {
		t.Fatalf("status.json keys = [%s], want exactly [chatId,generation,sequence,state,timestamp,version]", got)
	}
	if v, ok := obj["version"].(json.Number); !ok || v.String() != "1" {
		t.Errorf("version = %#v, want the number 1", obj["version"])
	}
	if obj["chatId"] != hookChatID {
		t.Errorf("chatId = %#v, want %q from VC_DESKTOP_CHAT_ID", obj["chatId"], hookChatID)
	}
	if g, ok := obj["generation"].(json.Number); !ok || g.String() != "3" {
		t.Errorf("generation = %#v, want the number 3 from VC_DESKTOP_STATUS_GENERATION", obj["generation"])
	}
	if obj["state"] != wantState {
		t.Errorf("state = %#v, want %q", obj["state"], wantState)
	}
	ts, ok := obj["timestamp"].(string)
	if !ok {
		t.Errorf("timestamp = %#v, want a string", obj["timestamp"])
	} else if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("timestamp %q is not an ISO/RFC 3339 time: %v", ts, err)
	}
	seqNum, ok := obj["sequence"].(json.Number)
	if !ok {
		t.Fatalf("sequence = %#v, want a number", obj["sequence"])
	}
	seq, err := seqNum.Int64()
	if err != nil {
		t.Fatalf("sequence %s is not an integer: %v", seqNum, err)
	}
	// Each hook is a new process, so the sequence has to come from something
	// that grows across processes — the clock. But the desktop reads it with
	// Number.isSafeInteger: nanoseconds since 1970 (~1.8e18) are far past
	// 2^53-1 (~9.0e15), JSON.parse rounds them, and the message is rejected.
	if seq < 1 || seq > maxSafeJSONInteger {
		t.Fatalf("sequence = %d, want an integer in [1, 2^53-1] — the desktop rejects anything else (Number.isSafeInteger)", seq)
	}
	return seq
}

func TestCodexHookCommand(t *testing.T) {
	h := newHookHarness(t)

	t.Run("outside the desktop it does nothing and succeeds", func(t *testing.T) {
		for name, input := range map[string]string{"UserPromptSubmit": userPromptInput, "Stop": stopInput, "SessionStart": sessionStartInput} {
			r, scratch := h.run(input, nil)
			wantQuietSuccess(t, "no VC_DESKTOP_STATUS_PATH, "+name, r)
			if files := regularFiles(t, scratch); len(files) != 0 {
				t.Errorf("no VC_DESKTOP_STATUS_PATH, %s: the CLI hook wrote %v", name, files)
			}
		}
	})

	t.Run("UserPromptSubmit reports Working", func(t *testing.T) {
		dir, status := h.channel()
		wantQuietSuccess(t, "UserPromptSubmit", first(h.run(userPromptInput, desktopHookEnv(status))))
		statusMessage(t, status, "Working")
		if got := strings.Join(hookDirNames(t, dir), ","); got != "status.json" {
			t.Fatalf("channel directory holds [%s], want only status.json (temporary files must be renamed away)", got)
		}
	})

	t.Run("Stop reports Ready with a later sequence", func(t *testing.T) {
		dir, status := h.channel()
		env := desktopHookEnv(status)
		wantQuietSuccess(t, "UserPromptSubmit", first(h.run(userPromptInput, env)))
		working := statusMessage(t, status, "Working")
		wantQuietSuccess(t, "Stop", first(h.run(stopInput, env)))
		ready := statusMessage(t, status, "Ready")
		if ready <= working {
			t.Fatalf("Stop wrote sequence %d after Working's %d; the desktop drops a sequence that does not grow", ready, working)
		}
		// And the next turn again, so the growth is not a one-off.
		wantQuietSuccess(t, "UserPromptSubmit again", first(h.run(userPromptInput, env)))
		if again := statusMessage(t, status, "Working"); again <= ready {
			t.Fatalf("the next turn wrote sequence %d after Ready's %d", again, ready)
		}
		if got := strings.Join(hookDirNames(t, dir), ","); got != "status.json" {
			t.Fatalf("channel directory holds [%s], want only status.json", got)
		}
	})

	t.Run("SessionStart records the Codex session beside the status", func(t *testing.T) {
		dir, status := h.channel()
		wantQuietSuccess(t, "SessionStart", first(h.run(sessionStartInput, desktopHookEnv(status))))
		session := filepath.Join(dir, "session.json")
		obj := readJSONObject(t, session)
		if got := objectKeys(obj); got != "chatId,runtime,sessionId,version" {
			t.Fatalf("session.json keys = [%s], want exactly [chatId,runtime,sessionId,version]", got)
		}
		if v, ok := obj["version"].(json.Number); !ok || v.String() != "1" {
			t.Errorf("version = %#v, want the number 1", obj["version"])
		}
		if obj["chatId"] != hookChatID {
			t.Errorf("chatId = %#v, want %q", obj["chatId"], hookChatID)
		}
		if obj["runtime"] != "codex" {
			t.Errorf("runtime = %#v, want \"codex\"", obj["runtime"])
		}
		if obj["sessionId"] != hookSessionID {
			t.Errorf("sessionId = %#v, want %q (session_id from Codex's input)", obj["sessionId"], hookSessionID)
		}
		for _, name := range hookDirNames(t, dir) {
			if name != "session.json" && name != "status.json" {
				t.Errorf("%s left in the channel directory; writes go through a temporary file and rename", name)
			}
		}
	})

	t.Run("a session id that is not a UUID is not recorded", func(t *testing.T) {
		dir, status := h.channel()
		input := strings.Replace(sessionStartInput, hookSessionID, "../../etc/passwd", 1)
		wantQuietSuccess(t, "SessionStart with a bad id", first(h.run(input, desktopHookEnv(status))))
		if names := hookDirNames(t, dir); len(names) != 0 {
			t.Fatalf("a non-UUID session id produced %v; the desktop hands this id to `vc desktop-session --codex-session`", names)
		}
	})

	t.Run("garbage on stdin writes nothing and still succeeds", func(t *testing.T) {
		for name, input := range map[string]string{
			"empty":        "",
			"not json":     "this is not json",
			"truncated":    `{"hook_event_name":"Stop"`,
			"array":        `["Stop"]`,
			"no event":     `{"session_id":"01a0ec63-35c1-7d02-bc6a-c06464a61565"}`,
			"event number": `{"hook_event_name":7}`,
		} {
			dir, status := h.channel()
			r, _ := h.run(input, desktopHookEnv(status))
			wantQuietSuccess(t, name, r)
			if names := hookDirNames(t, dir); len(names) != 0 {
				t.Errorf("%s: stdin %q wrote %v", name, input, names)
			}
			if strings.TrimSpace(r.stderr) == "" {
				t.Errorf("%s: a rejected input left no line on stderr; failures are silent to Codex but must not be to whoever reads its log", name)
			}
		}
	})

	t.Run("an event it does not handle writes nothing", func(t *testing.T) {
		dir, status := h.channel()
		wantQuietSuccess(t, "PreToolUse", first(h.run(preToolUseInput, desktopHookEnv(status))))
		if names := hookDirNames(t, dir); len(names) != 0 {
			t.Fatalf("PreToolUse wrote %v", names)
		}
	})

	t.Run("a malformed desktop environment writes nothing and still succeeds", func(t *testing.T) {
		for name, mutate := range map[string]func(env map[string]string, dir string){
			"generation not a number": func(env map[string]string, _ string) { env["VC_DESKTOP_STATUS_GENERATION"] = "three" },
			"generation zero":         func(env map[string]string, _ string) { env["VC_DESKTOP_STATUS_GENERATION"] = "0" },
			"chat id not a UUID":      func(env map[string]string, _ string) { env["VC_DESKTOP_CHAT_ID"] = "chat-1" },
			"relative status path":    func(env map[string]string, _ string) { env["VC_DESKTOP_STATUS_PATH"] = "status.json" },
		} {
			dir, status := h.channel()
			env := desktopHookEnv(status)
			mutate(env, dir)
			r, _ := h.run(userPromptInput, env)
			wantQuietSuccess(t, name, r)
			if names := hookDirNames(t, dir); len(names) != 0 {
				t.Errorf("%s: wrote %v", name, names)
			}
			if strings.TrimSpace(r.stderr) == "" {
				t.Errorf("%s: no line on stderr", name)
			}
		}
	})

	t.Run("an unwritable channel is reported, not fatal", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "closed-channel", "status.json")
		r, _ := h.run(stopInput, desktopHookEnv(missing))
		wantQuietSuccess(t, "Stop into a removed channel", r)
		if strings.TrimSpace(r.stderr) == "" {
			t.Error("a failed status write left no line on stderr")
		}
	})
}

// Codex runs it, not people: it is not advertised in `vc --help`.
func TestCodexHookIsAHiddenSubcommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"codex-hook"})
	if err != nil || cmd == nil || cmd.Name() != "codex-hook" {
		t.Fatalf("rootCmd has no codex-hook subcommand (found %v, %v)", cmd, err)
	}
	if !cmd.Hidden {
		t.Error("codex-hook is listed in vc --help; it is Codex's to call, not the person's")
	}
}
