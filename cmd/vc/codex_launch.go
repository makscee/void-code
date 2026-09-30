package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/codexruntime"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness/direct"
)

// codexGrantType is the provider type whose grant Codex runs on, the same one
// pi-bootstrap hands Pi as its codex transport.
const codexGrantType = "openai-codex-oauth"

// ensureCodexRuntime returns the pinned Codex, installing it into
// ~/.void-code/runtime/codex/<version> when missing; progress goes to w. A var
// so no test downloads 130–160 MB.
var ensureCodexRuntime = func(w io.Writer) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if home, err = filepath.EvalSymlinks(home); err != nil {
		return "", err
	}
	return codexruntime.Ensure(codexruntime.Options{
		Home:     home,
		BaseURL:  codexruntime.DefaultBaseURL,
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		Progress: w,
	})
}

// codexNoDaemon is the only argument Codex gets. Without --no-daemon the 0.158
// TUI starts a `codex app-server --managed-daemon` that outlives the session
// and needs a unix socket under CODEX_HOME, which a long home path breaks
// ("path must be shorter than SUN_LEN").
const codexNoDaemon = "--no-daemon"

// prepareCodexChild readies Codex. The grant is checked before the install,
// so nobody downloads Codex only to be refused; it comes from the providers
// cache when that is fresh and holds it, and from auth otherwise.
func prepareCodexChild(lc launchContext, notice string) (runtimeChild, error) {
	prepared, err := prepareCodex(lc.cfg, lc.token, "", os.Stderr, lc.client)
	if err != nil {
		return runtimeChild{}, err
	}
	// Pi shows the wallet notice inside its session; Codex has no such hook,
	// so it is printed before Codex takes the terminal.
	if notice != "" {
		fmt.Fprintln(os.Stderr, "vc: "+notice)
	}
	currentLaunchDiagnostics.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
	currentLaunchDiagnostics.flush()
	return runtimeChild{exe: prepared.exe, args: []string{codexNoDaemon}, env: prepared.env, refresh: prepared.refresh}, nil
}

// preparedCodex is an installed, configured Codex and the environment to run
// it in.
type preparedCodex struct {
	exe string
	env []string
	// refresh, when set, is the background refresh of the providers cache the
	// grant was taken from, to run once Codex is spawned.
	refresh func(context.Context)
}

// prepareCodex is the part of a Codex launch the CLI and the desktop share,
// in this order: the grant, the install, the managed config (trusting
// trustedFolder when it is set), the environment. Install progress goes to
// progress; auth is asked through client.
func prepareCodex(cfg config.Config, token, trustedFolder string, progress io.Writer, client *http.Client) (preparedCodex, error) {
	if cfg.CAOverride != "" {
		return preparedCodex{}, fmt.Errorf("Codex пока работает только с публичным релеем, а задан VC_RELAY_CA=%s. Уберите VC_RELAY_CA или переключитесь на Pi: vc runtime pi", cfg.CAOverride)
	}
	providerID, refresh, err := codexProviderID(cfg, token, client)
	if err != nil {
		return preparedCodex{}, err
	}
	codexPath, err := ensureCodexRuntime(progress)
	if err != nil {
		return preparedCodex{}, fmt.Errorf("не удалось установить Codex %s: %w\nПопробуйте ещё раз или переключитесь на Pi: vc runtime pi", codexruntime.Version, err)
	}
	cacheDir, err := config.CacheDir()
	if err != nil {
		return preparedCodex{}, err
	}
	codexHome := filepath.Join(cacheDir, "codex")
	if err := codexruntime.WriteConfigFor(codexHome, fmt.Sprintf("%s://%s", cfg.RelayScheme, cfg.RelayHost), trustedFolder); err != nil {
		return preparedCodex{}, err
	}
	env := buildCodexSpawnEnv(os.Environ(), codexHome, token, providerID, selfDir(), selfExecutablePath())
	return preparedCodex{exe: codexPath, env: env, refresh: refresh}, nil
}

// selfDir is the folder of the running vc, or "" when it cannot be told.
func selfDir() string {
	exe := selfExecutablePath()
	if exe == "" {
		return ""
	}
	return filepath.Dir(exe)
}

// selfExecutablePath is the absolute path of the running vc, or "" when it
// cannot be told.
func selfExecutablePath() string {
	exe, err := os.Executable()
	if err != nil || !filepath.IsAbs(exe) {
		return ""
	}
	return exe
}

// codexProviderID returns the first ChatGPT (openai-codex-oauth) grant, in
// the order auth lists them. A fresh providers cache holding one answers
// without the network, and then refresh is the background refresh to run once
// Codex is spawned; otherwise auth is asked live and its answer cached.
func codexProviderID(cfg config.Config, token string, client *http.Client) (string, func(context.Context), error) {
	if infos, ok := readFreshProviders(token, time.Now(), codexGrantType); ok {
		if id, found := grantOfType(infos, codexGrantType); found {
			return id, providersRefresher(cfg.AuthHost, token, client), nil
		}
	}
	infos, err := fetchAndCacheProviders(context.Background(), cfg.AuthHost, token, client)
	if err != nil {
		return "", nil, fmt.Errorf("не удалось получить доступы подписки: %w", err)
	}
	if id, found := grantOfType(infos, codexGrantType); found {
		return id, nil, nil
	}
	return "", nil, errors.New("Codex работает только с доступом к ChatGPT. Переключитесь на Pi: vc runtime pi")
}

// buildCodexSpawnEnv is the parent environment without anything that could
// point Codex elsewhere or hand it someone else's credential — OPENAI_API_KEY,
// OPENAI_BASE_URL, every CODEX_* and VC_* variable, and the relay/auth keys
// direct.PlainEnv strips — plus the values vc sets. PATH stays the
// person's own, with vc's folder put first so `!vc runtime pi` typed in Codex
// reaches this vc: Codex is a native binary and its tools run in the person's
// environment. VC_HOOK_EXE names this vc for the managed hooks
// (`"$VC_HOOK_EXE" codex-hook`), never a value inherited from the parent.
// Names are matched without regard to case, as Windows reads them.
func buildCodexSpawnEnv(parent []string, codexHome, token, providerID, vcDir, hookExe string) []string {
	base := direct.PlainEnv(parent)
	out := make([]string, 0, len(base)+6)
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if codexStrippedEnv(k) {
			continue
		}
		out = append(out, e)
	}
	out = withPathFirst(out, vcDir)
	out = append(out,
		"CODEX_HOME="+codexHome,
		"VC_AUTH_TOKEN="+token,
		"VC_CODEX_PROVIDER="+providerID,
		"VC_HARNESS=codex",
	)
	if hookExe != "" {
		out = append(out, "VC_HOOK_EXE="+hookExe)
	}
	return out
}

// withPathFirst puts dir at the front of env's PATH (whatever its case, as
// Windows writes Path), or makes it the PATH when there is none.
func withPathFirst(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	for i, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if strings.EqualFold(k, "PATH") {
			if v == "" {
				env[i] = k + "=" + dir
			} else {
				env[i] = k + "=" + dir + string(os.PathListSeparator) + v
			}
			return env
		}
	}
	return append(env, "PATH="+dir)
}

func codexStrippedEnv(key string) bool {
	upper := strings.ToUpper(key)
	return upper == "OPENAI_API_KEY" || upper == "OPENAI_BASE_URL" ||
		strings.HasPrefix(upper, "CODEX_") || strings.HasPrefix(upper, "VC_")
}
