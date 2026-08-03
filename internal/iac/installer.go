package iac

import "os/exec"

// PackageManager is a system package manager pvecli found on the machine's
// own PATH. Installing through it is offered, never assumed — see ensureTool
// in the cmd package for the confirmation gate this type exists to serve.
type PackageManager struct {
	// Name identifies the manager in the confirmation prompt.
	Name string
	// Install returns the full argv for installing one package, sudo included
	// where the manager needs root (every manager here except brew).
	Install func(pkg string) []string
}

// packageManagers is tried in this order. brew first: on macOS it never needs
// sudo, and where it coexists with a system manager (Homebrew on Linux) its
// formulae track upstream fastest.
var packageManagers = []PackageManager{
	{Name: "brew", Install: func(pkg string) []string { return []string{"brew", "install", pkg} }},
	{Name: "apt-get", Install: func(pkg string) []string { return []string{"sudo", "apt-get", "install", "-y", pkg} }},
	{Name: "dnf", Install: func(pkg string) []string { return []string{"sudo", "dnf", "install", "-y", pkg} }},
	{Name: "pacman", Install: func(pkg string) []string { return []string{"sudo", "pacman", "-S", "--noconfirm", pkg} }},
	{Name: "apk", Install: func(pkg string) []string { return []string{"sudo", "apk", "add", pkg} }},
}

// DetectPackageManager returns the first manager already on PATH.
//
// pvecli does not add a repository or import a signing key to make a package
// available. If the manager does not know the package — Terraform on stock
// apt or dnf, most commonly, which needs HashiCorp's own repo — it says so
// itself when asked to install it, and that real error is what reaches the
// operator, the same way Tool.Run relays terraform's and ansible's own output
// untouched.
func DetectPackageManager() (*PackageManager, bool) {
	for _, pm := range packageManagers {
		if _, err := exec.LookPath(pm.Name); err == nil {
			m := pm
			return &m, true
		}
	}
	return nil, false
}

// packageNames says what a pvecli-wrapped binary is called in a package
// manager's own repository, where it differs from the binary name itself.
var packageNames = map[string]string{
	AnsiblePlaybookBin: "ansible",
}

// PackageName maps a binary this package looks for to the package that
// provides it.
func PackageName(bin string) string {
	if pkg, ok := packageNames[bin]; ok {
		return pkg
	}
	return bin
}
