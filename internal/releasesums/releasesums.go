// Package releasesums downloads a release file and checks it against the
// release's SHA256SUMS before anyone uses it. The Pi runtime (internal/piruntime)
// and vc's self-update (internal/update) both install through it.
package releasesums

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxSumsBytes bounds a SHA256SUMS download; the real list is about 1 KB.
const MaxSumsBytes = 1 << 20

// Download fetches sumsURL and url, and returns url's body only when its
// SHA-256 matches the entry for name in the list. A missing list, a missing
// entry or a mismatch is a refusal. limit bounds the file's size.
func Download(client *http.Client, sumsURL, url, name string, limit int64) ([]byte, error) {
	sums, err := Fetch(client, sumsURL, MaxSumsBytes)
	if err != nil {
		return nil, err
	}
	want, ok := Lookup(sums, name)
	if !ok {
		return nil, fmt.Errorf("%s lists no %s", sumsURL, name)
	}
	data, err := Fetch(client, url, limit)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s: SHA-256 %s does not match the release's %s", url, got, want)
	}
	return data, nil
}

// Fetch GETs url and returns its body, refusing anything but 200 and bodies
// over limit bytes.
func Fetch(client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return data, nil
}

// Lookup finds name in sha256sum output ("<hex>  <name>" or "<hex> *<name>").
func Lookup(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}
