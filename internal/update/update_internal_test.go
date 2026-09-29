package update

import (
	"net/http"
	"testing"
)

// TestDefaultClientHasTimeouts guards the self-update against a server that
// never finishes: before void-board#419 it used http.DefaultClient, which
// waits forever.
func TestDefaultClientHasTimeouts(t *testing.T) {
	c := newClient()
	if c.Timeout != DownloadTimeout || c.Timeout <= 0 {
		t.Errorf("client timeout = %v; want DownloadTimeout (%v)", c.Timeout, DownloadTimeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T; want *http.Transport", c.Transport)
	}
	if tr.ResponseHeaderTimeout != responseHeaderTimeout || tr.ResponseHeaderTimeout <= 0 {
		t.Errorf("ResponseHeaderTimeout = %v; want %v", tr.ResponseHeaderTimeout, responseHeaderTimeout)
	}
	if tr.Proxy == nil {
		t.Error("the default client must keep honouring HTTPS_PROXY like http.DefaultTransport")
	}
}
