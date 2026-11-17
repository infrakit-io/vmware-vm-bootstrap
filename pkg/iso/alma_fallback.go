package iso

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// HTTPStatusError is returned when a download gets a non-200 response.
type HTTPStatusError struct{ Code int }

func (e *HTTPStatusError) Error() string { return fmt.Sprintf("download failed: HTTP %d", e.Code) }

const (
	almaPrimaryHost = "repo.almalinux.org"
	almaVaultHost   = "vault.almalinux.org"
	// almaTreeProbe is requested (HEAD) to check an install tree exists.
	almaTreeProbe = "repodata/repomd.xml"
)

var almaVersionedPathRe = regexp.MustCompile(`^/almalinux/(\d+\.\d+)/(.+)$`)

// AlmaVaultURL maps https://repo.almalinux.org/almalinux/<ver>/<rest> to
// https://vault.almalinux.org/<ver>/<rest>, where AlmaLinux moves a minor
// release once it is superseded. ok is false for any other URL (including
// the moving /almalinux/9/ alias, which has no vault equivalent).
func AlmaVaultURL(u string) (string, bool) {
	rest, found := strings.CutPrefix(u, "https://"+almaPrimaryHost)
	if !found {
		return "", false
	}
	m := almaVersionedPathRe.FindStringSubmatch(rest)
	if m == nil {
		return "", false
	}
	return "https://" + almaVaultHost + "/" + m[1] + "/" + m[2], true
}

// vaultURLFn is swapped in tests so no real network is needed.
var vaultURLFn = AlmaVaultURL

// isHTTPNotFound reports whether err carries an HTTP 404 from downloadFile.
func isHTTPNotFound(err error) bool {
	var he *HTTPStatusError
	return errors.As(err, &he) && he.Code == http.StatusNotFound
}

// ResolveAlmaTreeURL returns treeURL if it serves repodata, otherwise (HTTP 404
// only) the vault equivalent if that does. Other failures are returned as errors
// instead of guessing, so a broken network is not mistaken for a moved tree.
// The result is what gets rendered into the kickstart, because Anaconda (not
// Go) fetches the packages from it.
func ResolveAlmaTreeURL(ctx context.Context, client *http.Client, treeURL string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	code, err := headStatus(ctx, client, treeURL+almaTreeProbe)
	if err != nil {
		return "", err
	}
	if code == http.StatusOK {
		return treeURL, nil
	}
	if code != http.StatusNotFound {
		return "", fmt.Errorf("install tree %s: HTTP %d", treeURL, code)
	}
	vault, ok := vaultURLFn(treeURL)
	if !ok {
		return "", fmt.Errorf("install tree %s: HTTP 404 and no vault equivalent", treeURL)
	}
	if code, err = headStatus(ctx, client, vault+almaTreeProbe); err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("install tree %s: HTTP 404, vault %s: HTTP %d", treeURL, vault, code)
	}
	return vault, nil
}

func headStatus(ctx context.Context, client *http.Client, u string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("HEAD %s: %w", u, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
