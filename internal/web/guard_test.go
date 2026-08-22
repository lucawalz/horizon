package web

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/web/site"
)

const (
	interfaceSource = "site/src/lib/api.ts"
	scriptSuffix    = ".js"
)

var headerDeclaration = regexp.MustCompile(`export const interfaceHeader = '([^']+)'`)

// the header name is written once here and once in the interface, and a bundle that sets a different one refuses every mutation while every read still works
func TestTheBundleSetsTheHeaderTheGuardRequires(t *testing.T) {
	source, err := os.ReadFile(interfaceSource)
	if err != nil {
		t.Fatalf("read %s: %v", interfaceSource, err)
	}

	declaration := headerDeclaration.FindSubmatch(source)
	if declaration == nil {
		t.Fatalf("%s declares no interfaceHeader for the guard to agree with", interfaceSource)
	}
	if declared := string(declaration[1]); declared != interfaceHeader {
		t.Errorf("the interface sets %q and the guard requires %q", declared, interfaceHeader)
	}

	if site.DistDirFS == nil {
		t.Skip(interfaceAbsent)
	}
	if !bundleHolds(t, interfaceHeader) {
		t.Errorf("no script in the built bundle carries %q, so the shipped interface cannot pass the guard", interfaceHeader)
	}
}

func bundleHolds(t *testing.T, needle string) bool {
	t.Helper()

	held := false
	err := fs.WalkDir(site.DistDirFS, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path.Ext(name) != scriptSuffix {
			return err
		}
		script, err := fs.ReadFile(site.DistDirFS, name)
		if err != nil {
			return err
		}
		held = held || strings.Contains(string(script), needle)
		return nil
	})
	if err != nil {
		t.Fatalf("read the built bundle: %v", err)
	}
	return held
}
