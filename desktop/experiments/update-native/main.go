package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const maxRequest = 64 << 10

var transactionPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

type request struct {
	V             int      `json:"v"`
	Op            string   `json:"op"`
	TransactionID string   `json:"transactionId"`
	Capsule       string   `json:"capsule"`
	Identity      string   `json:"identity"`
	From          fromSpec `json:"from"`
	To            *toSpec  `json:"to,omitempty"`
	ReceiptDir    string   `json:"receiptDir"`
	BarrierDir    string   `json:"barrierDir"`
	DeadlineMS    int      `json:"deadlineMs"`
}

type fromSpec struct {
	Version            string `json:"version"`
	Target             string `json:"target"`
	Package            string `json:"package"`
	PredecessorReceipt string `json:"predecessorReceipt"`
	PredecessorPID     int    `json:"predecessorPid"`
}

type toSpec struct {
	Version string `json:"version"`
	Package string `json:"package"`
	Marker  string `json:"marker"`
}

type marker struct {
	V              int    `json:"v"`
	MarkerFile     string `json:"markerFile"`
	Capsule        string `json:"capsule"`
	TargetRel      string `json:"targetRel"`
	AppID          string `json:"appId"`
	ProductName    string `json:"productName"`
	ExecutableName string `json:"executableName"`
	RegistryGUID   string `json:"registryGuid"`
}

type receipt struct {
	V             int    `json:"v"`
	TransactionID string `json:"transactionId"`
	Identity      string `json:"identity"`
	Version       string `json:"version"`
	Arch          string `json:"arch"`
	ExecPath      string `json:"execPath"`
	ResourcesPath string `json:"resourcesPath"`
	Marker        string `json:"marker"`
	Bootstrap     string `json:"bootstrap"`
	PID           int    `json:"pid"`
	Packaged      bool   `json:"packaged"`
}

type journal struct {
	V                  int    `json:"v"`
	TransactionID      string `json:"transactionId"`
	Capsule            string `json:"capsule"`
	Identity           string `json:"identity"`
	Target             string `json:"target"`
	PredecessorPackage string `json:"predecessorPackage"`
	PredecessorSHA256  string `json:"predecessorSha256,omitempty"`
	PredecessorMarker  string `json:"predecessorMarker"`
	CandidatePackage   string `json:"candidatePackage"`
	CandidateMarker    string `json:"candidateMarker"`
	State              string `json:"state"`
}

type event struct {
	V             int    `json:"v"`
	Event         string `json:"event"`
	TransactionID string `json:"transactionId,omitempty"`
	Name          string `json:"name,omitempty"`
	Message       string `json:"message,omitempty"`
}

type bootstrap struct {
	TransactionID   string `json:"transactionId"`
	ReceiptDir      string `json:"receiptDir"`
	UserData        string `json:"userData"`
	ExitFile        string `json:"exitFile"`
	BootAttemptFile string `json:"bootAttemptFile"`
}

func main() {
	r, err := readRequest()
	if err == nil {
		err = execute(r)
	}
	if err != nil {
		emit(event{V: 1, Event: "failed", Message: err.Error()})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readRequest() (request, error) {
	var r request
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxRequest+1))
	if err != nil || len(data) > maxRequest {
		return r, errors.New("invalid or oversized request")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("invalid request: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return r, errors.New("exactly one JSON request is required")
	}
	return r, nil
}

func execute(r request) error {
	m, err := validateRequest(&r)
	if err != nil {
		return err
	}
	lock, err := acquireLock(filepath.Join(r.Capsule, "journals", "target.lock"))
	if err != nil {
		return fmt.Errorf("target transaction is already owned: %w", err)
	}
	defer lock.Close()
	if r.Op == "recover" {
		return recoverTransaction(r, m)
	}
	return installTransaction(r, m)
}

func validateRequest(r *request) (marker, error) {
	var m marker
	if r.V != 1 || (r.Op != "install" && r.Op != "recover") || !transactionPattern.MatchString(r.TransactionID) {
		return m, errors.New("unsupported request")
	}
	if r.DeadlineMS < 100 || r.DeadlineMS > 120000 || r.From.Version != "1.0.0" || r.From.PredecessorPID < 1 {
		return m, errors.New("invalid request bounds or predecessor")
	}
	if r.Op == "install" && (r.To == nil || r.To.Version != "1.0.1" || r.To.Marker == "") {
		return m, errors.New("install requires the fixed successor")
	}
	if r.Op == "recover" && r.To != nil {
		return m, errors.New("recover must not contain a successor")
	}
	capsule, err := canonicalDirectory(r.Capsule)
	if err != nil || !samePath(capsule, r.Capsule) {
		return m, errors.New("capsule is not canonical")
	}
	if err := decodeStrictFile(filepath.Join(capsule, ".native-update-capsule.json"), &m); err != nil {
		return m, fmt.Errorf("invalid capsule marker: %w", err)
	}
	targetRel := "installed.app"
	if runtime.GOOS == "windows" {
		targetRel = "installed"
	}
	if m.V != 1 || m.MarkerFile != ".native-update-capsule.json" || !samePath(m.Capsule, capsule) || m.TargetRel != targetRel ||
		m.AppID != r.Identity || !strings.HasPrefix(m.AppID, "org.voidcode.fixture.") || m.ProductName == "" || m.ExecutableName == "" || m.RegistryGUID == "" {
		return m, errors.New("capsule identity fence failed")
	}
	r.Capsule = capsule
	expectedTarget := filepath.Join(capsule, targetRel)
	if !samePath(r.From.Target, expectedTarget) || !samePath(r.ReceiptDir, filepath.Join(capsule, "receipts")) || !samePath(r.BarrierDir, filepath.Join(capsule, "barriers")) {
		return m, errors.New("request paths do not match capsule ABI")
	}
	if err := requireContainedArtifact(r.From.Package, filepath.Join(capsule, "packages")); err != nil {
		return m, err
	}
	if err := requireContainedRegular(r.From.PredecessorReceipt, r.ReceiptDir); err != nil {
		return m, err
	}
	if r.To != nil {
		if err := requireContainedArtifact(r.To.Package, filepath.Join(capsule, "packages")); err != nil {
			return m, err
		}
	}
	var pre receipt
	if err := decodeStrictFile(r.From.PredecessorReceipt, &pre); err != nil || pre.V != 1 || pre.Bootstrap != "ok" || !pre.Packaged || pre.PID != r.From.PredecessorPID ||
		pre.Identity != r.Identity || pre.Version != r.From.Version || pre.Arch != fixtureArch() || pre.ExecPath == "" || pre.ResourcesPath == "" || pre.Marker == "" {
		return m, errors.New("invalid predecessor receipt/PID")
	}
	if processAlive(pre.PID) {
		return m, errors.New("recorded predecessor is still alive")
	}
	if r.Op == "install" {
		if err := validateInstalledPredecessor(*r, m, pre); err != nil {
			return m, err
		}
	}
	return m, nil
}

func validateInstalledPredecessor(r request, m marker, pre receipt) error {
	info, err := os.Lstat(r.From.Target)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("target is absent, redirected, or not a directory")
	}
	actualTarget, err := filepath.EvalSymlinks(r.From.Target)
	if err != nil || !samePath(actualTarget, r.From.Target) {
		return errors.New("target redirect refused")
	}
	execPath, resourcesPath := installedPaths(r.From.Target, m)
	actualExec, err1 := filepath.EvalSymlinks(execPath)
	actualResources, err2 := filepath.EvalSymlinks(resourcesPath)
	if err1 != nil || err2 != nil || !samePath(actualExec, pre.ExecPath) || !samePath(actualResources, pre.ResourcesPath) {
		return errors.New("predecessor canonical paths do not match target")
	}
	var identity marker
	if err := decodeStrictFile(filepath.Join(resourcesPath, "identity.json"), &identity); err != nil || identity.AppID != m.AppID || identity.ExecutableName != m.ExecutableName || identity.RegistryGUID != m.RegistryGUID {
		return errors.New("installed target identity mismatch")
	}
	content, err := os.ReadFile(filepath.Join(resourcesPath, "full-resource-marker.txt"))
	if err != nil || string(content) != pre.Marker {
		return errors.New("installed predecessor marker mismatch")
	}
	return nil
}

func installTransaction(r request, m marker) error {
	preMarker, err := os.ReadFile(filepath.Join(installedResources(r.From.Target), "full-resource-marker.txt"))
	if err != nil {
		return err
	}
	j := journal{V: 1, TransactionID: r.TransactionID, Capsule: r.Capsule, Identity: r.Identity, Target: r.From.Target,
		PredecessorPackage: r.From.Package, PredecessorMarker: string(preMarker), CandidatePackage: r.To.Package, CandidateMarker: r.To.Marker, State: "prepared"}
	if runtime.GOOS == "windows" {
		j.PredecessorSHA256, err = fileSHA256(r.From.Package)
		if err != nil {
			return err
		}
	} else if err := stageBundle(r, m); err != nil {
		return err
	}
	journalPath := filepath.Join(r.Capsule, "journals", r.TransactionID+".json")
	if err := atomicJSON(journalPath, j); err != nil {
		return fmt.Errorf("persist journal: %w", err)
	}
	if err := barrier(r, "before-mutation"); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err := runNSIS(r.To.Package, r.From.Target, r.Capsule, r.TransactionID); err != nil {
			j.State = "installer-failed"
			_ = atomicJSON(journalPath, j)
			return fmt.Errorf("successor installer failed: %w", err)
		}
		if err := validateBundleIdentity(r.From.Target, m, r.To.Marker); err != nil {
			j.State = "installer-failed"
			_ = atomicJSON(journalPath, j)
			return fmt.Errorf("installed successor failed identity validation: %w", err)
		}
	} else {
		backup := filepath.Join(r.Capsule, "backup.app")
		if pathExists(backup) {
			return errors.New("unclaimed backup already exists")
		}
		if err := os.Rename(r.From.Target, backup); err != nil {
			return err
		}
		j.State = "old-in-backup"
		if err := atomicJSON(journalPath, j); err != nil {
			return err
		}
		if err := barrier(r, "after-old-to-backup"); err != nil {
			return err
		}
		if err := os.Rename(stagePath(r), r.From.Target); err != nil {
			return err
		}
		j.State = "candidate-in-target"
		if err := atomicJSON(journalPath, j); err != nil {
			return err
		}
		if err := barrier(r, "after-new-to-target-before-commit"); err != nil {
			return err
		}
	}
	if err := launchAndValidate(r, m); err != nil {
		j.State = "receipt-failed"
		_ = atomicJSON(journalPath, j)
		return err
	}
	j.State = "completed"
	return atomicJSON(journalPath, j)
}

func recoverTransaction(r request, m marker) error {
	path := filepath.Join(r.Capsule, "journals", r.TransactionID+".json")
	var j journal
	if err := decodeStrictFile(path, &j); err != nil {
		return fmt.Errorf("recovery requires an intact journal: %w", err)
	}
	if j.V != 1 || j.TransactionID != r.TransactionID || !samePath(j.Capsule, r.Capsule) || j.Identity != r.Identity || !samePath(j.Target, r.From.Target) ||
		!samePath(j.PredecessorPackage, r.From.Package) || j.PredecessorMarker == "" || j.CandidateMarker == "" || !knownJournalState(j.State) {
		return errors.New("journal fence mismatch")
	}
	if err := requireContainedArtifact(j.CandidatePackage, filepath.Join(r.Capsule, "packages")); err != nil {
		return errors.New("journal candidate fence mismatch")
	}
	// A receipt is process evidence, never permission to replace a live app. A
	// malformed transaction receipt is also an uncertainty and therefore a refusal.
	transactionReceipt := filepath.Join(r.ReceiptDir, r.TransactionID+".json")
	if pathExists(transactionReceipt) {
		var successor receipt
		if err := decodeStrictFile(transactionReceipt, &successor); err != nil || successor.V != 1 || successor.TransactionID != r.TransactionID || successor.Identity != r.Identity || successor.PID < 1 {
			return errors.New("uncertain successor receipt")
		}
		if processAlive(successor.PID) {
			return errors.New("recorded successor is still alive")
		}
	}
	if runtime.GOOS == "windows" {
		digest, err := fileSHA256(j.PredecessorPackage)
		if err != nil || digest != j.PredecessorSHA256 {
			return errors.New("retained predecessor installer changed")
		}
		if err := runNSIS(j.PredecessorPackage, j.Target, r.Capsule, r.TransactionID+"-recovery"); err != nil {
			return err
		}
	} else {
		if err := recoverMac(j, m); err != nil {
			return err
		}
	}
	j.State = "recovered"
	return atomicJSON(path, j)
}

func knownJournalState(state string) bool {
	switch state {
	case "prepared", "old-in-backup", "candidate-in-target", "installer-failed", "receipt-failed", "completed", "recovered":
		return true
	default:
		return false
	}
}

func recoverMac(j journal, m marker) error {
	backup := filepath.Join(j.Capsule, "backup.app")
	if pathExists(backup) {
		if err := validateBundleIdentity(backup, m, j.PredecessorMarker); err != nil {
			return fmt.Errorf("untrusted backup: %w", err)
		}
		if pathExists(j.Target) {
			if err := validateBundleIdentity(j.Target, m, j.CandidateMarker); err != nil {
				return fmt.Errorf("untrusted recovery target: %w", err)
			}
			if err := os.RemoveAll(j.Target); err != nil {
				return err
			}
		}
		if err := os.Rename(backup, j.Target); err != nil {
			return err
		}
	} else {
		if err := validateBundleIdentity(j.Target, m, j.PredecessorMarker); err != nil {
			return fmt.Errorf("predecessor is not recoverable: %w", err)
		}
	}
	if pathExists(filepath.Join(j.Capsule, "journals", j.TransactionID+".new.app")) {
		_ = os.RemoveAll(filepath.Join(j.Capsule, "journals", j.TransactionID+".new.app"))
	}
	return nil
}

func stageBundle(r request, m marker) error {
	if err := validateBundleIdentity(r.To.Package, m, r.To.Marker); err != nil {
		return fmt.Errorf("invalid candidate bundle: %w", err)
	}
	stage := stagePath(r)
	if pathExists(stage) {
		return errors.New("transaction stage already exists")
	}
	return copyTree(r.To.Package, stage)
}

func stagePath(r request) string {
	return filepath.Join(r.Capsule, "journals", r.TransactionID+".new.app")
}

func validateBundleIdentity(root string, m marker, expectedMarker string) error {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("bundle root is not a real directory")
	}
	resources := installedResources(root)
	var identity marker
	if err := decodeStrictFile(filepath.Join(resources, "identity.json"), &identity); err != nil {
		return err
	}
	if identity.AppID != m.AppID || identity.ExecutableName != m.ExecutableName || identity.RegistryGUID != m.RegistryGUID {
		return errors.New("bundle identity mismatch")
	}
	value, err := os.ReadFile(filepath.Join(resources, "full-resource-marker.txt"))
	if err != nil || string(value) != expectedMarker {
		return errors.New("bundle marker mismatch")
	}
	return nil
}

func barrier(r request, name string) error {
	e := event{V: 1, Event: "barrier", TransactionID: r.TransactionID, Name: name}
	path := filepath.Join(r.BarrierDir, r.TransactionID+"."+name+".json")
	if err := atomicJSON(path, e); err != nil {
		return err
	}
	emit(e)
	ack := filepath.Join(r.BarrierDir, r.TransactionID+"."+name+".continue")
	end := time.Now().Add(time.Duration(r.DeadlineMS) * time.Millisecond)
	for time.Now().Before(end) {
		if info, err := os.Lstat(ack); err == nil && info.Mode().IsRegular() {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("barrier %s timed out", name)
}

func launchAndValidate(r request, m marker) error {
	configPath := filepath.Join(r.ReceiptDir, r.TransactionID+".bootstrap.json")
	b := bootstrap{TransactionID: r.TransactionID, ReceiptDir: r.ReceiptDir, UserData: filepath.Join(r.Capsule, "userdata"),
		ExitFile: filepath.Join(r.ReceiptDir, r.TransactionID+".exit"), BootAttemptFile: filepath.Join(r.ReceiptDir, r.TransactionID+".boot.json")}
	if err := atomicJSON(configPath, b); err != nil {
		return err
	}
	execPath, resourcesPath := installedPaths(r.From.Target, m)
	if err := startFixture(execPath, "--fixture-bootstrap="+configPath, fixtureEnvironment(r.Capsule, r.TransactionID)); err != nil {
		return err
	}
	receiptPath := filepath.Join(r.ReceiptDir, r.TransactionID+".json")
	end := time.Now().Add(time.Duration(r.DeadlineMS) * time.Millisecond)
	for time.Now().Before(end) {
		var got receipt
		if decodeStrictFile(receiptPath, &got) == nil && validateSuccessReceipt(got, r, execPath, resourcesPath) == nil {
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return errors.New("no valid successor receipt before deadline")
}

func validateSuccessReceipt(got receipt, r request, execPath, resourcesPath string) error {
	actualExec, err1 := filepath.EvalSymlinks(execPath)
	actualResources, err2 := filepath.EvalSymlinks(resourcesPath)
	gotExec, err3 := filepath.EvalSymlinks(got.ExecPath)
	gotResources, err4 := filepath.EvalSymlinks(got.ResourcesPath)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || filepath.Clean(got.ExecPath) != got.ExecPath || filepath.Clean(got.ResourcesPath) != got.ResourcesPath ||
		got.V != 1 || got.TransactionID != r.TransactionID || got.Identity != r.Identity || got.Version != r.To.Version ||
		got.Arch != fixtureArch() || !samePath(gotExec, actualExec) || !samePath(gotResources, actualResources) || got.Marker != r.To.Marker ||
		got.Bootstrap != "ok" || !got.Packaged || got.PID < 1 || !processAlive(got.PID) {
		return errors.New("invalid successor receipt")
	}
	markerBytes, err := os.ReadFile(filepath.Join(resourcesPath, "full-resource-marker.txt"))
	if err != nil || string(markerBytes) != r.To.Marker {
		return errors.New("successor resource marker mismatch")
	}
	return nil
}

func installedPaths(target string, m marker) (string, string) {
	if runtime.GOOS == "windows" {
		return filepath.Join(target, m.ExecutableName+".exe"), filepath.Join(target, "resources")
	}
	return filepath.Join(target, "Contents", "MacOS", m.ExecutableName), filepath.Join(target, "Contents", "Resources")
}
func installedResources(target string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(target, "resources")
	}
	return filepath.Join(target, "Contents", "Resources")
}
func fixtureArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

func canonicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path is not absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("not a real directory")
	}
	return filepath.EvalSymlinks(path)
}
func requireContainedRegular(path, root string) error {
	if !filepath.IsAbs(path) || !contained(path, root) {
		return errors.New("file escapes capsule namespace")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("expected regular file")
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || !contained(actual, root) {
		return errors.New("file redirect escapes capsule")
	}
	return nil
}
func requireContainedArtifact(path, root string) error {
	if !filepath.IsAbs(path) || !contained(path, root) {
		return errors.New("artifact escapes capsule namespace")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return errors.New("invalid artifact")
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || !contained(actual, root) {
		return errors.New("artifact redirect escapes capsule")
	}
	return nil
}
func contained(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func samePath(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func pathExists(path string) bool { _, err := os.Lstat(path); return err == nil }

func decodeStrictFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxRequest {
		return errors.New("missing or oversized JSON file")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}
func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = atomicReplace(tmp, path); err != nil {
		return err
	}
	ok = true
	if dir, openErr := os.Open(filepath.Dir(path)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
func emit(e event) { data, _ := json.Marshal(e); _, _ = os.Stdout.Write(append(data, '\n')) }
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fixtureEnvironment(capsule, transaction string) []string {
	home := filepath.Join(capsule, "sandbox", transaction+"-helper")
	for _, p := range []string{filepath.Join(home, "AppData", "Roaming"), filepath.Join(home, "AppData", "Local"), filepath.Join(home, "Temp")} {
		_ = os.MkdirAll(p, 0700)
	}
	result := make([]string, 0, len(os.Environ())+6)
	for _, item := range os.Environ() {
		key := strings.SplitN(item, "=", 2)[0]
		upper := strings.ToUpper(key)
		api := strings.Index(upper, "API")
		containsAPIKey := api >= 0 && strings.Contains(upper[api+3:], "KEY")
		if strings.HasPrefix(upper, "VC_") || containsAPIKey || upper == "ELECTRON_RUN_AS_NODE" || upper == "HOME" || upper == "USERPROFILE" || upper == "APPDATA" || upper == "LOCALAPPDATA" || upper == "TEMP" || upper == "TMP" || upper == "TMPDIR" {
			continue
		}
		result = append(result, item)
	}
	return append(result, "HOME="+home, "USERPROFILE="+home, "APPDATA="+filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"), "TEMP="+filepath.Join(home, "Temp"), "TMP="+filepath.Join(home, "Temp"), "TMPDIR="+filepath.Join(home, "Temp"))
}
