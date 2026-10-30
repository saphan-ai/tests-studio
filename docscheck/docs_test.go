// Package docscheck keeps the public documents of this repository honest: the README, SECURITY.md
// and CONTRIBUTING.md have the sections a stranger expects, in order; every command they show runs;
// every number they print carries the command that produced it and is recomputed here; the two
// licence files are the standard texts; and no document names a host or an address it should not.
//
// Run it with: go -C docscheck test ./... -count=1
package docscheck

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	apacheSHA256 = "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4"
	ccBySHA256   = "9ba9550ad48438d0836ddab3da480b3b69ffa0aac7b7878b5a0039e7ab429411"

	advisoryURL = "https://github.com/saphan-ai/tests-studio/security/advisories/new"

	commandTimeout = 10 * time.Minute
)

// The sections of each document, in the order a reader meets them.
var (
	readmeSections = []string{
		"The problem",
		"How it works",
		"See a real example",
		"Make your test framework report to Tests Studio",
		"What is measured",
		"Honest limits",
		"Built in public",
		"Repository layout",
		"Documentation",
		"For technical readers",
	}
	securitySections = []string{
		"What it treats as untrusted",
		"What it protects",
		"How",
		"Weaknesses found and fixed",
		"What is still being checked",
		"Reporting a vulnerability",
	}
	contributingSections = []string{
		"Reporting",
		"Your test framework is missing?",
		"Building and testing",
		"How a change is made here",
	}
)

// The markers a fenced command may carry (a trailing `# needs: <marker>`) instead of being run.
const (
	needsListener = "listener" // runs only where the machine allows a loopback listener
	needsAdapter  = "adapter"  // holds a placeholder for the reader's own adapter command
	needsSelf     = "self"     // is this check itself
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("%s is missing: write it (%v)", name, err)
	}
	return string(b)
}

// h2s returns the "## " headings of a document with their line numbers, ignoring fenced code.
func h2s(doc string) (names []string, lines []int) {
	inFence := false
	sc := bufio.NewScanner(strings.NewReader(doc))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		l := sc.Text()
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
			continue
		}
		if !inFence && strings.HasPrefix(l, "## ") {
			names = append(names, strings.TrimSpace(strings.TrimPrefix(l, "## ")))
			lines = append(lines, n)
		}
	}
	return names, lines
}

// checkSections fails when a required section is missing, and names the two sections that stand in
// the wrong order when all are present.
func checkSections(t *testing.T, file string, required []string) {
	t.Helper()
	names, _ := h2s(readDoc(t, file))
	pos := map[string]int{}
	for i, n := range names {
		if _, dup := pos[n]; !dup {
			pos[n] = i
		}
	}
	var missing []string
	for _, r := range required {
		if _, ok := pos[r]; !ok {
			missing = append(missing, strconv.Quote(r))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s lacks the section(s) %s: add each as a \"## \" heading", file, strings.Join(missing, ", "))
	}
	for i := 1; i < len(required); i++ {
		if pos[required[i-1]] > pos[required[i]] {
			t.Errorf("%s: section %q must come before section %q: move one of them", file, required[i-1], required[i])
		}
	}
}

func TestReadmeFirstScreen(t *testing.T) {
	doc := readDoc(t, "README.md")
	lines := strings.Split(doc, "\n")
	if len(lines) == 0 || lines[0] != "# Tests Studio" {
		t.Fatalf("README.md must open with the line \"# Tests Studio\"")
	}
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, "## ") {
			t.Fatalf("README.md has no bold sentence before its first section: the first screen must say what this is in one bold sentence")
		}
		if strings.HasPrefix(l, "**") {
			return
		}
	}
	t.Fatalf("README.md has no bold sentence on its first screen")
}

func TestReadmeSections(t *testing.T)   { checkSections(t, "README.md", readmeSections) }
func TestSecuritySections(t *testing.T) { checkSections(t, "SECURITY.md", securitySections) }
func TestContributingSections(t *testing.T) {
	checkSections(t, "CONTRIBUTING.md", contributingSections)
}

func TestSecurityReportingAddress(t *testing.T) {
	if !strings.Contains(readDoc(t, "SECURITY.md"), advisoryURL) {
		t.Fatalf("SECURITY.md must give the private reporting address %s", advisoryURL)
	}
}

// section returns the body of one "## " section (up to the next "## " heading).
func section(t *testing.T, file, name string) string {
	t.Helper()
	var out []string
	in, inFence := false, false
	for _, l := range strings.Split(readDoc(t, file), "\n") {
		if strings.HasPrefix(l, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(l, "## ")) == name
			continue
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// freshCopy copies every file git tracks or would track (not the ignored ones) into a new directory,
// which stands for a fresh clone of this tree with the working changes in it.
func freshCopy(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files failed, so there is no tree to copy: run this check inside a git checkout (%v)", err)
	}
	dst := t.TempDir()
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		src := filepath.Join(root, rel)
		info, err := os.Stat(src)
		if err != nil || info.IsDir() {
			continue // a tracked file deleted in the working tree
		}
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		to := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func loopbackAllowed() bool {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func shell(dir, line string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

type command struct {
	line   string // the command, without its marker
	needs  string // "" when it must run
	number int    // the line in the document
}

var needsRe = regexp.MustCompile(`\s+#\s*needs:\s*([a-z]+)\s*$`)

// fencedCommands returns every command line of the fenced blocks tagged sh.
func fencedCommands(doc string) []command {
	var cmds []command
	inSh, inOther := false, false
	for i, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "```") {
			if inSh || inOther {
				inSh, inOther = false, false
			} else if strings.TrimSpace(strings.TrimPrefix(l, "```")) == "sh" {
				inSh = true
			} else {
				inOther = true
			}
			continue
		}
		if !inSh || strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		c := command{line: strings.TrimSpace(l), number: i + 1}
		if m := needsRe.FindStringSubmatch(l); m != nil {
			c.needs = m[1]
			c.line = strings.TrimSpace(strings.TrimSuffix(l, m[0]))
		}
		cmds = append(cmds, c)
	}
	return cmds
}

func TestReadmeCommandsRun(t *testing.T) {
	cmds := fencedCommands(readDoc(t, "README.md"))
	if len(cmds) == 0 {
		t.Fatalf("README.md shows no command in a fenced block tagged sh: a stranger needs commands to copy")
	}
	hasListenerRun := false
	for _, c := range cmds {
		if c.needs == needsListener {
			hasListenerRun = true
		}
	}
	if !hasListenerRun {
		t.Fatalf("README.md never shows the conformance suite run (a command marked `# needs: listener`)")
	}
	dir := freshCopy(t)
	loopback := loopbackAllowed()
	if !loopback && os.Getenv("TESTS_DOCS_REQUIRE_SOCKETS") == "1" {
		t.Fatalf("loopback is denied here and TESTS_DOCS_REQUIRE_SOCKETS=1: the commands that need a listener were not measured")
	}
	ran, unrun := 0, 0
	for _, c := range cmds {
		switch c.needs {
		case needsAdapter, needsSelf:
			continue
		case needsListener:
			if !loopback {
				unrun++
				t.Logf("UNRUN README.md:%d needs a loopback listener, which this machine denies: %s", c.number, c.line)
				continue
			}
		case "":
		default:
			t.Errorf("README.md:%d: unknown marker `# needs: %s` (known: %s, %s, %s)", c.number, c.needs, needsListener, needsAdapter, needsSelf)
			continue
		}
		ran++
		if out, err := shell(dir, c.line); err != nil {
			t.Errorf("README.md:%d: the command %q failed in a fresh copy of the tree: %v\n%s", c.number, c.line, err, out)
		}
	}
	t.Logf("README.md: %d commands run, %d unrun for want of a listener, %d marked adapter or self", ran, unrun, len(cmds)-ran-unrun)
}

var (
	isoDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	digitRe   = regexp.MustCompile(`\d+`)
	spanRe    = regexp.MustCompile("`([^`]+)`")
)

var boldNumberRe = regexp.MustCompile(`^- \*\*(\d+)\*\*`)

// TestMeasuredNumbers requires every item of the README's "What is measured" list to read
// - **N** what it counts: `command`, runs the command, and compares its first integer with N.
func TestMeasuredNumbers(t *testing.T) {
	body := section(t, "README.md", "What is measured")
	if strings.TrimSpace(body) == "" {
		t.Fatalf("README.md has no \"What is measured\" section")
	}
	dir := freshCopy(t)
	items := 0
	for _, l := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(l)
		spans := spanRe.FindAllStringSubmatch(trim, -1)
		if !strings.HasPrefix(trim, "- ") {
			if digitRe.MatchString(isoDateRe.ReplaceAllString(trim, "")) {
				t.Errorf("README.md \"What is measured\": the line %q carries a number outside a measured item: write it as - **N** what it counts: `command`", trim)
			}
			continue
		}
		nm := boldNumberRe.FindStringSubmatch(trim)
		if nm == nil || len(spans) == 0 {
			t.Errorf("README.md \"What is measured\": the item %q must read - **N** what it counts: `command`", trim)
			continue
		}
		want, _ := strconv.Atoi(nm[1])
		cmd := spans[len(spans)-1][1]
		items++
		out, err := shell(dir, cmd)
		if err != nil {
			t.Errorf("README.md \"What is measured\": the command %q failed: %v\n%s", cmd, err, out)
			continue
		}
		got, err := strconv.Atoi(digitRe.FindString(out))
		if err != nil || got != want {
			t.Errorf("README.md \"What is measured\": the item says %d but `%s` prints %q: recompute and correct the number", want, cmd, strings.TrimSpace(out))
		}
	}
	if items == 0 {
		t.Errorf("README.md \"What is measured\" has no item of the form - **N** what it counts: `command`")
	}
}

func sha(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("%s is missing: add the standard licence text (%v)", name, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestLicenceFiles(t *testing.T) {
	if got := sha(t, "LICENSE"); got != apacheSHA256 {
		t.Errorf("LICENSE hashes to %s, not the standard Apache-2.0 text %s: restore the unmodified text", got, apacheSHA256)
	}
	if got := sha(t, "LICENSE-docs"); got != ccBySHA256 {
		t.Errorf("LICENSE-docs hashes to %s, not the CC BY 4.0 legal code %s: restore the unmodified text", got, ccBySHA256)
	}
	readme := readDoc(t, "README.md")
	for _, want := range []string{"LICENSE-docs", "Apache-2.0", "CC BY 4.0"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.md does not say which licence covers which files: it never mentions %q", want)
		}
	}
}

var (
	ipv4Re = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	urlRe  = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://([^/\s)>\x60"'\]]+)`)
	// a bare dotted name whose last label is letters: file names with a known extension are skipped
	bareRe = regexp.MustCompile(`\b([a-z0-9][a-z0-9-]*(?:\.[a-z0-9-]+)*\.[a-z][a-z-]*[a-z])\b`)

	allowedNets = []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("127.0.0.0/8"),
	}
	fileExt = map[string]bool{
		"md": true, "go": true, "json": true, "yaml": true, "yml": true, "sh": true, "mod": true, "sum": true,
		"txt": true, "py": true, "ts": true, "js": true, "toml": true, "xml": true, "html": true, "css": true,
		"proto": true, "sig": true, "tar": true, "gz": true, "zip": true, "log": true, "out": true, "exe": true,
		"cfg": true, "ini": true, "env": true, "lock": true, "bin": true,
	}
)

// identifiers are dotted names of the specification that are not host names: schema and event kinds.
var identifiers = map[string]bool{
	"tests.event": true, "tests.run-result": true, "mocks.event": true,
	"run.opened": true, "run.closed": true, "step.started": true, "step.finished": true, "evidence.stored": true,
}

func hostAllowed(host string) bool {
	h := strings.ToLower(host)
	if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.Contains(h[i:], ".") {
		h = h[:i]
	}
	return h == "example.com" || strings.HasSuffix(h, ".example.com") || h == "localhost" ||
		strings.HasSuffix(h, ".example") || strings.HasSuffix(h, ".test") || h == "example" || h == "test"
}

// TestNoStrangerHosts reads the three public documents for an address or a host name that should not
// be there. github.com is allowed only inside github.com/saphan-ai/.
func TestNoStrangerHosts(t *testing.T) {
	for _, file := range []string{"README.md", "SECURITY.md", "CONTRIBUTING.md"} {
		doc := readDoc(t, file)
		for n, l := range strings.Split(doc, "\n") {
			for _, ip := range ipv4Re.FindAllString(l, -1) {
				a, err := netip.ParseAddr(ip)
				if err != nil {
					continue // not an address, e.g. 999.1.1.1 inside a version string
				}
				ok := false
				for _, p := range allowedNets {
					ok = ok || p.Contains(a)
				}
				if !ok {
					t.Errorf("%s:%d: the address %s is outside the documentation and loopback ranges: use 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 or 127.0.0.0/8", file, n+1, ip)
				}
			}
			rest := l
			for _, m := range urlRe.FindAllStringSubmatchIndex(l, -1) {
				host := l[m[2]:m[3]]
				tail := l[m[3]:]
				if strings.EqualFold(host, "github.com") && strings.HasPrefix(tail, "/saphan-ai/") {
					continue
				}
				if !hostAllowed(host) {
					t.Errorf("%s:%d: the host %q is not allowed: use example.com, a .example or .test name, localhost, or github.com/saphan-ai/", file, n+1, host)
				}
			}
			rest = urlRe.ReplaceAllString(rest, " ")
			rest = strings.ReplaceAll(rest, "github.com/saphan-ai/", " ")
			for _, m := range bareRe.FindAllString(rest, -1) {
				last := m[strings.LastIndexByte(m, '.')+1:]
				if fileExt[last] || identifiers[m] || hostAllowed(m) {
					continue
				}
				if _, err := netip.ParseAddr(m); err == nil {
					continue
				}
				t.Errorf("%s:%d: the name %q looks like a host name: use example.com, a .example or .test name, localhost, or github.com/saphan-ai/ (a file name with an unusual extension? add the extension to fileExt)", file, n+1, m)
			}
		}
	}
}

var shaRe = regexp.MustCompile("`([0-9a-f]{7})`")

// TestSecurityCommitsExist requires every commit SECURITY.md cites (a seven-hex span in backticks)
// to be a commit of this repository.
func TestSecurityCommitsExist(t *testing.T) {
	doc := readDoc(t, "SECURITY.md")
	ms := shaRe.FindAllStringSubmatch(doc, -1)
	if len(ms) == 0 {
		t.Fatalf("SECURITY.md cites no commit: each weakness found and fixed names the commit that fixed it, as a seven-character hash in backticks")
	}
	for _, m := range ms {
		out, err := exec.Command("git", "-C", repoRoot(t), "cat-file", "-t", m[1]+"^{commit}").CombinedOutput()
		if err != nil {
			t.Errorf("SECURITY.md cites the commit %s, which this repository does not hold: %s", m[1], strings.TrimSpace(string(out)))
		}
	}
}
