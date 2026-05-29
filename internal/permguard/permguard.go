// Package permguard is a Go port of wcz234/claude-auto-mode-bridge's rule
// engine: a local PreToolUse permission classifier that lets Claude Code
// auto mode run shell commands on a non-Anthropic backend without the
// server-side safety classifier. Policy = vendor rules.json, byte-for-byte.
//
// Evaluation order: deny rules first (any match → deny), then allow rules
// (any match → allow), then LLM fallback (if enabled), else default-allow.
package permguard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed rules.json
var rulesJSON []byte

// Event is the subset of the CC PreToolUse stdin payload we evaluate.
type Event struct {
	ToolName string // tool_name
	Command  string // tool_input.command (Bash) — empty for non-Bash tools
	FilePath string // tool_input.file_path (Edit/Write/MultiEdit)
	Cwd      string // cwd from the event
}

// Decision is the result of Classify.
type Decision struct {
	Decision string // "allow" or "deny"
	Reason   string
}

// rulesDoc mirrors the top-level structure of rules.json.
type rulesDoc struct {
	Allow       []rule      `json:"allow"`
	Deny        []rule      `json:"deny"`
	LLMFallback llmFallback `json:"llm_fallback"`
}

type rule struct {
	Name        string   `json:"name"`
	Tools       []string `json:"tools"`
	Pattern     string   `json:"pattern"`
	PathCheck   string   `json:"path_check"`
	AllowIfContains []string `json:"allow_if_contains"`
	Reason      string   `json:"reason"`
}

type llmFallback struct {
	Enabled        bool   `json:"enabled"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Prompt         string `json:"prompt"`
}

// Guard holds compiled rules. Load once; Classify is goroutine-safe.
type Guard struct {
	doc          rulesDoc
	denyCompiled []*regexp.Regexp
	allowCompiled []*regexp.Regexp
	// classifier is called when rules are ambiguous; nil → fail-open (allow).
	classifier func(ev Event, fb llmFallback) Decision
}

// Load parses the embedded rules.json and compiles regexes.
func Load() (*Guard, error) {
	var doc rulesDoc
	if err := json.Unmarshal(rulesJSON, &doc); err != nil {
		return nil, fmt.Errorf("permguard: parse rules.json: %w", err)
	}
	g := &Guard{doc: doc}
	// Pre-compile deny patterns.
	g.denyCompiled = make([]*regexp.Regexp, len(doc.Deny))
	for i, r := range doc.Deny {
		if r.Pattern == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("permguard: compile deny[%d] pattern %q: %w", i, r.Pattern, err)
		}
		g.denyCompiled[i] = re
	}
	// Pre-compile allow patterns.
	g.allowCompiled = make([]*regexp.Regexp, len(doc.Allow))
	for i, r := range doc.Allow {
		if r.Pattern == "" {
			continue
		}
		re, err := regexp.Compile("(?i)" + r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("permguard: compile allow[%d] pattern %q: %w", i, r.Pattern, err)
		}
		g.allowCompiled[i] = re
	}
	return g, nil
}

// SetClassifier injects an LLM fallback classifier. Pass nil to clear (rules-only + fail-open).
func (g *Guard) SetClassifier(fn func(ev Event, fb llmFallback) Decision) {
	g.classifier = fn
}

// LLMFallback exposes the fallback config so the hook can wire the relay classifier.
func (g *Guard) LLMFallback() llmFallback { return g.doc.LLMFallback }

// Classify applies the rule engine and returns a decision.
func (g *Guard) Classify(ev Event) Decision {
	home, _ := os.UserHomeDir()

	// 1. Deny rules first.
	for i, r := range g.doc.Deny {
		if !toolMatches(r.Tools, ev.ToolName) {
			continue
		}
		if r.Pattern != "" {
			text := commandOrPath(ev, r.Tools)
			if g.denyCompiled[i] == nil || !g.denyCompiled[i].MatchString(text) {
				continue
			}
		}
		if r.PathCheck != "" {
			if !checkPathCondition(r.PathCheck, ev.FilePath, ev.Cwd, home) {
				continue
			}
		}
		return Decision{Decision: "deny", Reason: r.Reason}
	}

	// 2. Allow rules.
	for i, r := range g.doc.Allow {
		if !toolMatches(r.Tools, ev.ToolName) {
			continue
		}
		if r.Pattern != "" {
			text := commandOrPath(ev, r.Tools)
			if g.allowCompiled[i] == nil || !g.allowCompiled[i].MatchString(text) {
				continue
			}
		}
		if r.PathCheck != "" {
			if !checkPathCondition(r.PathCheck, ev.FilePath, ev.Cwd, home) {
				continue
			}
		}
		if len(r.AllowIfContains) > 0 {
			matched := false
			for _, af := range r.AllowIfContains {
				if strings.Contains(ev.Command, af) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		return Decision{Decision: "allow", Reason: r.Reason}
	}

	// 3. LLM fallback.
	if g.doc.LLMFallback.Enabled && g.classifier != nil {
		return g.classifier(ev, g.doc.LLMFallback)
	}

	// 4. Default: allow (fail-open).
	return Decision{Decision: "allow", Reason: "default allow"}
}

// toolMatches checks whether the event's tool name is in the rule's tools list.
func toolMatches(tools []string, toolName string) bool {
	for _, t := range tools {
		if strings.EqualFold(t, toolName) {
			return true
		}
	}
	return false
}

// commandOrPath selects the text to match against a pattern.
// For Bash rules → command; for Edit/Write/MultiEdit → file_path.
func commandOrPath(ev Event, tools []string) string {
	for _, t := range tools {
		if strings.EqualFold(t, "Bash") {
			return ev.Command
		}
	}
	return ev.FilePath
}

// norm converts a path to forward-slash absolute form for comparison.
func norm(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return filepath.ToSlash(abs)
}

// checkPathCondition is a faithful Go port of the Python check_path_condition.
func checkPathCondition(pathCheck, filePath, cwd, home string) bool {
	if filePath == "" {
		return false
	}
	normPath := norm(filePath)
	normHome := filepath.ToSlash(home)

	switch pathCheck {
	case "in_project":
		normCwd := norm(cwd)
		return strings.HasPrefix(normPath, normCwd) || strings.HasPrefix(normPath, norm(cwd))

	case "in_memory_dir":
		memBase := filepath.ToSlash(filepath.Join(home, ".claude", "projects"))
		return strings.Contains(normPath, memBase) && strings.Contains(normPath, "/memory/")

	case "is_system_path":
		systemPrefixes := []string{
			"/etc/", "/usr/", "/System/", "/Windows/", "/Program Files/",
			"/ProgramData/", "/boot/", "/sbin/", "/lib/", "/lib64/",
			"C:/Windows/", "C:/Program Files/", "C:/Program Files (x86)/",
			"C:/ProgramData/",
		}
		sensitiveHome := []string{
			normHome + "/.ssh/",
			normHome + "/.gnupg/",
			normHome + "/.aws/",
			normHome + "/.kube/",
		}
		lp := strings.ToLower(normPath)
		for _, pfx := range systemPrefixes {
			if strings.HasPrefix(lp, strings.ToLower(pfx)) || strings.HasPrefix(normPath, pfx) {
				return true
			}
		}
		for _, sh := range sensitiveHome {
			if strings.HasPrefix(normPath, sh) {
				return true
			}
		}
		return false

	case "is_agent_config":
		agentPaths := []string{"/.claude/settings", "/CLAUDE.md"}
		for _, acp := range agentPaths {
			if strings.Contains(normPath, acp) {
				return true
			}
		}
		return false

	case "is_package_dir":
		packageDirs := []string{
			"/node_modules/", "/site-packages/", "/vendor/", "/.venv/",
			"/pnpm-store/", "/.yarn/", "/.cache/", "/__pycache__/",
			"/bower_components/", "/.gradle/", "/.m2/",
		}
		for _, pd := range packageDirs {
			if strings.Contains(normPath, pd) {
				return true
			}
		}
		return false
	}
	return false
}
