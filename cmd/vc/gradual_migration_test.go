package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

func TestGradualIdentityMigration(t *testing.T) {
	legacy := "legacy-secret-byte-exact\n"
	identity := "identity-session.identity-secret"
	subject := "88888888-8888-4888-8888-888888888888"
	base := func() (*string, *bytes.Buffer, migrationDeps) {
		stored := legacy
		out := &bytes.Buffer{}
		deps := migrationDeps{
			loadCurrent: func() (string, bool, error) { return stored, false, nil },
			loadLegacy:  func() (string, error) { return "", auth.ErrNotLoggedIn },
			legacyMe: func(got string) (auth.MeResult, error) {
				if got != legacy {
					t.Fatal("wrong legacy proof")
				}
				return auth.MeResult{UserID: subject}, nil
			},
			start: func(string) (auth.MigrationStartResult, error) {
				return auth.MigrationStartResult{Exchange: "opaque-exchange"}, nil
			},
			promptOTP: func() (string, error) { return "246810", nil },
			complete: func(_, proof, code string) (auth.MigrationCompleteResult, error) {
				if proof != legacy || code != "246810" {
					t.Fatal("wrong completion proof")
				}
				return auth.MigrationCompleteResult{Token: identity, SubjectID: subject, Audience: "vc"}, nil
			},
			relayMe: func(got string) (string, error) {
				if got != legacy && got != identity {
					t.Fatal("wrong relay bearer")
				}
				return subject, nil
			},
			save:   func(value string) error { stored = value; return nil },
			device: func() error { return errors.New("unexpected device flow") }, out: out,
		}
		return &stored, out, deps
	}

	t.Run("success installs only after every gate", func(t *testing.T) {
		stored, out, deps := base()
		if err := runGradualLogin(config.Config{}, deps); err != nil {
			t.Fatal(err)
		}
		if *stored != identity {
			t.Fatal("identity credential not installed")
		}
		if strings.Contains(out.String(), legacy) || strings.Contains(out.String(), identity) || strings.Contains(out.String(), "246810") || strings.Contains(out.String(), "@") {
			t.Fatalf("secret or email output: %q", out.String())
		}
	})

	failures := []struct {
		name  string
		alter func(*migrationDeps)
		want  string
	}{
		{"cancellation", func(d *migrationDeps) { d.promptOTP = func() (string, error) { return "", os.ErrClosed } }, "unchanged"},
		{"invalid OTP", func(d *migrationDeps) {
			d.complete = func(string, string, string) (auth.MigrationCompleteResult, error) {
				return auth.MigrationCompleteResult{}, auth.ErrMigrationInvalidOTP
			}
		}, "invalid"},
		{"missing or unverified email", func(d *migrationDeps) {
			d.start = func(string) (auth.MigrationStartResult, error) {
				return auth.MigrationStartResult{}, auth.ErrMigrationUnavailable
			}
		}, "contact Maks"},
		{"conflict", func(d *migrationDeps) {
			d.complete = func(string, string, string) (auth.MigrationCompleteResult, error) {
				return auth.MigrationCompleteResult{}, auth.ErrMigrationConflict
			}
		}, "Contact Maks"},
		{"source unavailable", func(d *migrationDeps) {
			d.start = func(string) (auth.MigrationStartResult, error) {
				return auth.MigrationStartResult{}, auth.ErrMigrationSource
			}
		}, "temporarily unavailable"},
		{"complete subject mismatch", func(d *migrationDeps) {
			old := d.complete
			d.complete = func(a, b, c string) (auth.MigrationCompleteResult, error) {
				r, e := old(a, b, c)
				r.SubjectID = "other-subject"
				return r, e
			}
		}, "Contact Maks"},
		{"relay denial", func(d *migrationDeps) {
			d.relayMe = func(token string) (string, error) {
				if token == legacy {
					return subject, nil
				}
				return "", auth.ErrMigrationSource
			}
		}, "temporarily unavailable"},
		{"relay subject mismatch", func(d *migrationDeps) {
			d.relayMe = func(token string) (string, error) {
				if token == legacy {
					return subject, nil
				}
				return "other-subject", nil
			}
		}, "Contact Maks"},
		{"entitlement denial", func(d *migrationDeps) {
			d.relayMe = func(token string) (string, error) {
				if token == legacy {
					return subject, nil
				}
				return "", auth.ErrEntitlementDenied
			}
		}, "active VC subscription"},
		{"persistence failure", func(d *migrationDeps) { d.save = func(string) error { return os.ErrPermission } }, "Could not save"},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			stored, out, deps := base()
			tc.alter(&deps)
			err := runGradualLogin(config.Config{}, deps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
			if *stored != legacy {
				t.Fatalf("credential changed: %q", *stored)
			}
			combined := out.String() + err.Error()
			for _, secret := range []string{legacy, identity, "246810", "opaque-exchange", "private@example.test"} {
				if strings.Contains(combined, secret) {
					t.Fatalf("output leaked secret: %q", combined)
				}
			}
		})
	}

	for _, payload := range []struct {
		name string
		body string
	}{
		{"relay trailing garbage", `{"subject_id":"` + subject + `"}garbage`},
		{"relay second JSON object", `{"subject_id":"` + subject + `"}{"subject_id":"` + subject + `"}`},
	} {
		t.Run(payload.name+" preserves credential", func(t *testing.T) {
			stored, _, deps := base()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(payload.body))
			}))
			defer server.Close()
			deps.relayMe = func(token string) (string, error) {
				if token == legacy {
					return subject, nil
				}
				return auth.FetchRelayMe(server.URL, token, server.Client())
			}
			if err := runGradualLogin(config.Config{}, deps); err == nil {
				t.Fatal("malformed relay response accepted")
			}
			if *stored != legacy {
				t.Fatalf("credential changed: %q", *stored)
			}
		})
	}

	t.Run("inactive legacy subscription preserves credential", func(t *testing.T) {
		stored, _, deps := base()
		called := false
		deps.relayMe = func(string) (string, error) { return "", auth.ErrEntitlementDenied }
		deps.device = func() error { called = true; return nil }
		err := runGradualLogin(config.Config{}, deps)
		if err == nil || !strings.Contains(err.Error(), "active VC subscription") || called {
			t.Fatalf("err=%v called=%v", err, called)
		}
		if *stored != legacy {
			t.Fatal("credential changed")
		}
	})
	t.Run("no legacy uses device flow", func(t *testing.T) {
		called := false
		deps := migrationDeps{loadCurrent: func() (string, bool, error) { return "", false, auth.ErrNotLoggedIn }, loadLegacy: func() (string, error) { return "", auth.ErrNotLoggedIn }, device: func() error { called = true; return nil }}
		if err := runGradualLogin(config.Config{}, deps); err != nil || !called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})
}
