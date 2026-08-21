package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// runStatusJSON resolves auth state the same way runStatus does — auth.Load,
// then auth.FetchMe against cfg.AuthHost — and writes exactly one JSON object
// to out. It builds the object from the raw values, not from status.go's
// lipgloss-rendered strings: those carry ANSI escape codes that a GUI would
// display literally.
func runStatusJSON(cfg config.Config, out io.Writer) error {
	obj := map[string]any{}

	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		obj["authState"] = "signed_out"
		return json.NewEncoder(out).Encode(obj)
	}

	me, err := auth.FetchMe(cfg.AuthHost, strings.TrimSpace(token), &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		obj["authState"] = "invalid_credential"
		obj["error"] = err.Error()
		return json.NewEncoder(out).Encode(obj)
	}

	identity := me.UserID
	if me.Email != "" {
		identity = me.Email
	}

	obj["authState"] = "signed_in"
	obj["identity"] = identity
	// pct/resetAt are only set when the server actually returned them — a
	// zero value here would read as "no budget left" instead of "no budget
	// information available".
	if me.Pct != nil {
		obj["pct"] = *me.Pct
	}
	if me.ResetAt != "" {
		obj["resetAt"] = me.ResetAt
	}
	return json.NewEncoder(out).Encode(obj)
}
