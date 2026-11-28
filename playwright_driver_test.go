package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Playwright driver is shipped inside the Docker image at build time, but the
// version the binary demands at runtime is decided by the playwright-go module in
// go.mod. Those are two independent sources of truth, and when they disagree the
// build still succeeds: the failure only surfaces once a scraping job runs inside
// a container, as
//
//	could not install driver: driver exists but version not <x> in : /opt/ms-playwright-go
//
// That happened in issue #312, where the module graph contained two different
// playwright-go modules at once (github.com/mxschmitt/playwright-go, demanding
// driver 1.61.1, and github.com/playwright-community/playwright-go pulled in
// indirectly by scrapemate, demanding 1.60.0) while both read the same
// PLAYWRIGHT_DRIVER_PATH. The tests below fail the build instead.

// playwrightModuleRe matches a require line in go.mod for any module whose path
// contains "playwright-go", capturing the module path, the version, and the rest
// of the line (used to tell requirements from replace directives).
var playwrightModuleRe = regexp.MustCompile(`(?m)^[ \t]*(?:require[ \t]+)?(\S*playwright-go)[ \t]+(v\S+)(.*)$`)

// playwrightModule is a playwright-go module required by go.mod.
type playwrightModule struct {
	path    string
	version string
}

func (m playwrightModule) String() string {
	return m.path + " " + m.version
}

// playwrightModulesIn returns every playwright-go module required by the given
// go.mod contents.
func playwrightModulesIn(gomod string) []playwrightModule {
	matches := playwrightModuleRe.FindAllStringSubmatch(gomod, -1)
	mods := make([]playwrightModule, 0, len(matches))

	for _, match := range matches {
		// replace directives carry their own syntax and are not requirements.
		if strings.Contains(match[3], "=>") {
			continue
		}

		mods = append(mods, playwrightModule{path: match[1], version: match[2]})
	}

	return mods
}

// checkUniqueDriverModule reports why mods is not a single, unambiguous
// playwright-go requirement, or "" when it is.
func checkUniqueDriverModule(mods []playwrightModule) string {
	switch {
	case len(mods) == 0:
		return "go.mod requires no playwright-go module; expected exactly one"
	case len(mods) > 1:
		got := make([]string, 0, len(mods))
		for _, mod := range mods {
			got = append(got, mod.String())
		}

		return "go.mod requires " + strings.Join(got, " and ") +
			"; expected exactly one. Each module pins its own Playwright driver " +
			"version but they all read PLAYWRIGHT_DRIVER_PATH, so only one can be " +
			"satisfied at runtime. Align the dependency (usually by bumping " +
			"scrapemate) so a single module remains."
	default:
		return ""
	}
}

var (
	playwrightArgRe     = regexp.MustCompile(`(?m)^\s*ARG\s+PLAYWRIGHT_GO_VERSION=(\S+)`)
	playwrightInstallRe = regexp.MustCompile(`go install\s+(\S*playwright-go)/cmd/playwright@(\S+)`)
)

// checkDockerfileDriver reports every way the Dockerfile would install a driver
// that the binary built from want would reject.
func checkDockerfileDriver(dockerfile string, want playwrightModule) []string {
	var problems []string

	argMatch := playwrightArgRe.FindStringSubmatch(dockerfile)
	if argMatch == nil {
		return []string{"Dockerfile does not define ARG PLAYWRIGHT_GO_VERSION; " +
			"the driver version can no longer be checked against go.mod"}
	}

	if argMatch[1] != want.version {
		problems = append(problems, "Dockerfile ARG PLAYWRIGHT_GO_VERSION="+argMatch[1]+
			" but go.mod requires "+want.String()+
			". The image would ship a driver the binary rejects at runtime.")
	}

	installMatch := playwrightInstallRe.FindStringSubmatch(dockerfile)
	if installMatch == nil {
		return append(problems, "Dockerfile does not install the playwright CLI via "+
			"`go install <module>/cmd/playwright@<version>`; update this test if the "+
			"install method changed")
	}

	// The driver is installed from a module path too, and that must be the same
	// module the binary links, not merely the same version number.
	if installMatch[1] != want.path {
		problems = append(problems, "Dockerfile installs the driver from "+installMatch[1]+
			" but go.mod requires "+want.path+
			". Different modules pin different driver versions even at the same tag.")
	}

	// Accept either the ARG indirection or a literal version, but if it is literal
	// it still has to match go.mod.
	if ref := installMatch[2]; !strings.Contains(ref, "PLAYWRIGHT_GO_VERSION") && ref != want.version {
		problems = append(problems, "Dockerfile installs the driver at "+ref+
			" but go.mod requires "+want.version+".")
	}

	return problems
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()

	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("could not read %s: %v", name, err)
	}

	return string(raw)
}

// TestPlaywrightDriverModuleIsUnique guards against a second playwright-go module
// entering the build graph. Two modules mean two required driver versions sharing
// a single PLAYWRIGHT_DRIVER_PATH, which only one of them can satisfy.
func TestPlaywrightDriverModuleIsUnique(t *testing.T) {
	if problem := checkUniqueDriverModule(playwrightModulesIn(readRepoFile(t, "go.mod"))); problem != "" {
		t.Error(problem)
	}
}

// TestPlaywrightDockerfileVersionMatchesGoMod guards against the Docker image
// baking in a driver that the compiled binary does not accept.
func TestPlaywrightDockerfileVersionMatchesGoMod(t *testing.T) {
	mods := playwrightModulesIn(readRepoFile(t, "go.mod"))
	if len(mods) != 1 {
		t.Skip("module graph is ambiguous; TestPlaywrightDriverModuleIsUnique reports this")
	}

	for _, problem := range checkDockerfileDriver(readRepoFile(t, "Dockerfile"), mods[0]) {
		t.Error(problem)
	}
}

// TestPlaywrightDriftIsDetected pins the checks against the dependency state that
// actually shipped the issue #312 failure, so the guards cannot silently decay
// into always passing.
func TestPlaywrightDriftIsDetected(t *testing.T) {
	// Abridged from go.mod at commit 63902df, the last release carrying the bug.
	const issue312GoMod = `require (
	github.com/gosom/scrapemate v1.2.2
	github.com/mxschmitt/playwright-go v0.6100.0
)

require (
	github.com/playwright-community/playwright-go v0.6000.0 // indirect
)
`

	mods := playwrightModulesIn(issue312GoMod)
	if len(mods) != 2 {
		t.Fatalf("expected to parse 2 playwright modules from the issue #312 go.mod, got %d: %v", len(mods), mods)
	}

	if checkUniqueDriverModule(mods) == "" {
		t.Error("the issue #312 dependency state (two playwright-go modules) was not reported")
	}

	// A Dockerfile pinned to a version go.mod does not require must also be caught.
	const driftedDockerfile = `ARG PLAYWRIGHT_GO_VERSION=v0.6100.0
RUN go install github.com/mxschmitt/playwright-go/cmd/playwright@${PLAYWRIGHT_GO_VERSION}
`

	want := playwrightModule{path: "github.com/mxschmitt/playwright-go", version: "v0.6000.0"}
	if len(checkDockerfileDriver(driftedDockerfile, want)) == 0 {
		t.Error("a Dockerfile pinning a driver version absent from go.mod was not reported")
	}

	// The same tag on a different module is still a mismatch.
	wrongModule := playwrightModule{path: "github.com/playwright-community/playwright-go", version: "v0.6100.0"}
	if len(checkDockerfileDriver(driftedDockerfile, wrongModule)) == 0 {
		t.Error("a Dockerfile installing the driver from a different module was not reported")
	}
}

// TestPlaywrightModuleParsingIgnoresReplace makes sure a replace directive is not
// counted as a second requirement, which would fail the guard spuriously.
func TestPlaywrightModuleParsingIgnoresReplace(t *testing.T) {
	const gomod = `require github.com/mxschmitt/playwright-go v0.6100.0

replace github.com/mxschmitt/playwright-go v0.6100.0 => ../playwright-go

replace (
	github.com/playwright-community/playwright-go v0.6000.0 => github.com/mxschmitt/playwright-go v0.6100.0
)
`

	mods := playwrightModulesIn(gomod)
	if len(mods) != 1 {
		t.Fatalf("expected 1 requirement, got %d: %v", len(mods), mods)
	}

	if want := "github.com/mxschmitt/playwright-go v0.6100.0"; mods[0].String() != want {
		t.Errorf("got %q, want %q", mods[0].String(), want)
	}
}
