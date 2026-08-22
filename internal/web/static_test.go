package web

import (
	"bytes"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/web/site"
)

const (
	mountElement    = `id="root"`
	wireNoStore     = "no-store"
	wireJSONType    = "application/json; charset=utf-8"
	wireHTMLType    = "text/html; charset=utf-8"
	wireSVGType     = "image/svg+xml"
	wirePNGType     = "image/png"
	staleAssetPath  = "/assets/index-deadbeef.js"
	missingIconPath = "/favicon.ico"
	dottedLeasePath = "/leases/foo.bar"
	scriptExtension = ".js"
	styleExtension  = ".css"
	svgExtension    = ".svg"
	pngExtension    = ".png"
	pngMagic        = "\x89PNG\r\n\x1a\n"
	svgRootTag      = "<svg"
	iconRelAttr     = `rel="icon"`
	listingLink     = "<a href="
)

var iconContentTypes = map[string]string{
	svgExtension: wireSVGType,
	pngExtension: wirePNGType,
}

var (
	iconLinkPattern = regexp.MustCompile(`<link\b[^>]*` + iconRelAttr + `[^>]*>`)
	hrefAttrPattern = regexp.MustCompile(`\bhref="([^"]+)"`)
)

func iconHrefs(t *testing.T, shell []byte) []string {
	t.Helper()
	var hrefs []string
	for _, tag := range iconLinkPattern.FindAll(shell, -1) {
		match := hrefAttrPattern.FindSubmatch(tag)
		if match == nil {
			t.Fatalf("a <link %s> tag carries no href: %s", iconRelAttr, tag)
		}
		hrefs = append(hrefs, string(match[1]))
	}
	return hrefs
}

func baseMediaType(t *testing.T, header string) string {
	t.Helper()
	base, _, err := mime.ParseMediaType(header)
	if err != nil {
		t.Fatalf("parse content type %q: %v", header, err)
	}
	return base
}

func TestEmbeddedAssetsAreServed(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, "/")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), mountElement) {
		t.Errorf("the shell omits %s, want the mount element", mountElement)
	}

	var scripts, styles int
	var oneScript string
	err := fs.WalkDir(site.DistDirFS, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return nil
		case path.Ext(name) == scriptExtension:
			scripts++
			oneScript = name
		case path.Ext(name) == styleExtension:
			styles++
		default:
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded bundle: %v", err)
	}
	if scripts == 0 || styles == 0 {
		t.Fatalf("the embedded bundle holds %d scripts and %d stylesheets, want at least one of each", scripts, styles)
	}

	served := get(t, server, "/"+oneScript)
	if served.Code != http.StatusOK {
		t.Errorf("/%s status = %d, want %d", oneScript, served.Code, http.StatusOK)
	}
	if served.Body.Len() == 0 {
		t.Errorf("/%s served an empty body", oneScript)
	}
}

func TestAStaleAssetReferenceIsRefusedRatherThanShelled(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, staleAssetPath)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if strings.Contains(response.Body.String(), mountElement) {
		t.Error("a missing asset served the shell, want a refusal a module loader can report")
	}
}

func TestAMissingStaticAssetIsRefusedRatherThanShelled(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, missingIconPath)
	if response.Code != http.StatusNotFound {
		t.Fatalf("%s status = %d, want %d", missingIconPath, response.Code, http.StatusNotFound)
	}
	if strings.Contains(response.Body.String(), mountElement) {
		t.Errorf("%s served the application shell, want a refusal a browser can report", missingIconPath)
	}
}

func TestTheAssetDirectoryIsRefusedRatherThanListed(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, assetPrefix)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	body := response.Body.String()
	if strings.Contains(body, listingLink) || strings.Contains(body, scriptExtension) {
		t.Errorf("%s listed the bundle, want a refusal:\n%s", assetPrefix, body)
	}
}

func TestDeepLinksServeTheApplicationShell(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	for _, target := range []string{"/leases/batch-run", "/machines", "/nowhere"} {
		response := get(t, server, target)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", target, response.Code, http.StatusOK)
			continue
		}
		if !strings.Contains(response.Body.String(), mountElement) {
			t.Errorf("%s does not serve the shell", target)
		}
	}
}

func TestALeaseNameContainingADotStillReceivesTheShell(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, dottedLeasePath)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want %d", dottedLeasePath, response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), mountElement) {
		t.Errorf("%s does not serve the shell, want a dotted lease name to route like any other deep link", dottedLeasePath)
	}
}

func TestTheShellIsNotStored(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, "/")
	sent := sentHeaders(response)
	if got := sent.Get("Cache-Control"); got != wireNoStore {
		t.Errorf("Cache-Control = %q, want %q so a hashed asset reference cannot outlive the shell", got, wireNoStore)
	}
	if got := sent.Get("Content-Type"); got != wireHTMLType {
		t.Errorf("Content-Type = %q, want %q", got, wireHTMLType)
	}
}

func TestIconsAreServedAsThemselvesNotTheShell(t *testing.T) {
	if site.DistDirFS == nil {
		t.Skip("no interface is embedded under this build tag")
	}

	shell, err := fs.ReadFile(site.DistDirFS, shellFile)
	if err != nil {
		t.Fatalf("read the shipped shell: %v", err)
	}

	hrefs := iconHrefs(t, shell)
	if len(hrefs) == 0 {
		t.Fatal(`the shell carries no <link rel="icon">, want at least one so a browser tab is not left blank`)
	}

	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	for _, href := range hrefs {
		t.Run(strings.TrimPrefix(path.Ext(href), "."), func(t *testing.T) {
			response := get(t, server, href)
			if response.Code != http.StatusOK {
				t.Fatalf("%s status = %d, want %d", href, response.Code, http.StatusOK)
			}

			body := response.Body.Bytes()
			if len(body) == 0 {
				t.Fatalf("%s served an empty body", href)
			}
			if strings.Contains(string(body), mountElement) {
				t.Fatalf("%s served the application shell instead of the icon", href)
			}

			wantType, known := iconContentTypes[path.Ext(href)]
			if !known {
				t.Fatalf("%s has an extension this test does not know how to verify, want one of %v", href, iconContentTypes)
			}
			if got := baseMediaType(t, sentHeaders(response).Get("Content-Type")); got != wantType {
				t.Errorf("%s Content-Type = %q, want %q", href, got, wantType)
			}

			switch path.Ext(href) {
			case svgExtension:
				if !bytes.Contains(body, []byte(svgRootTag)) {
					t.Errorf("%s does not carry an %s root element", href, svgRootTag)
				}
			case pngExtension:
				if !bytes.HasPrefix(body, []byte(pngMagic)) {
					t.Errorf("%s does not carry the PNG signature", href)
				}
			}
		})
	}
}

func TestUnknownAPIPathsReportAJSONError(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, "/api/nowhere")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if strings.Contains(response.Body.String(), mountElement) {
		t.Fatal("an unmatched api path served the shell, want a json failure")
	}
	failure := decodeBody[apiError](t, response)
	if failure.Status != http.StatusNotFound {
		t.Errorf("body status = %d, want %d", failure.Status, http.StatusNotFound)
	}
	if want := http.StatusText(http.StatusNotFound); failure.Title != want {
		t.Errorf("title = %q, want %q", failure.Title, want)
	}
}

func TestASiteWithoutABundleReportsTheAbsentInterface(t *testing.T) {
	for name, handler := range map[string]http.Handler{
		"shell":  siteHandler(nil),
		"assets": bundleFiles(nil),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

			if recorder.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusNotFound)
			}
			if recorder.Body.Len() == 0 {
				t.Error("a build without the interface answered with an empty body, want a reason")
			}
		})
	}
}

func TestJSONResponsesAreNotStored(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	response := get(t, server, "/api/leases")
	sent := sentHeaders(response)
	if got := sent.Get("Cache-Control"); got != wireNoStore {
		t.Errorf("Cache-Control = %q, want %q", got, wireNoStore)
	}
	if got := sent.Get("Content-Type"); got != wireJSONType {
		t.Errorf("Content-Type = %q, want %q", got, wireJSONType)
	}
}
