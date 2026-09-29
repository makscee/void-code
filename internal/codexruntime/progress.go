package codexruntime

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// leftoverPrefixes name what an interrupted install leaves beside the install
// folder: a partial download, a half-unpacked stage, a rolled-back old tree.
var leftoverPrefixes = []string{".codex-download-", ".codex-stage-", ".codex-backup-"}

// clearLeftovers removes what an earlier, interrupted install left in parent
// (a killed download is ~100 MB that nothing else would ever remove). It is
// best effort: a leftover that cannot be removed does not stop the install.
func clearLeftovers(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		for _, prefix := range leftoverPrefixes {
			if strings.HasPrefix(e.Name(), prefix) {
				_ = os.RemoveAll(filepath.Join(parent, e.Name()))
				break
			}
		}
	}
}

// percentWriter reports the downloaded share every 10%, on one terminal line
// rewritten with "\r"; the line is closed with "\n" at 100%.
type percentWriter struct {
	out   io.Writer
	total int64
	done  int64
	next  int64
}

func (p *percentWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	pct := p.done * 100 / p.total
	if pct > 100 {
		pct = 100
	}
	if pct >= p.next {
		fmt.Fprintf(p.out, "\rvc: Codex загружен на %d%%", pct)
		if pct == 100 {
			fmt.Fprintln(p.out)
		}
		p.next = pct - pct%10 + 10
	}
	return len(b), nil
}
