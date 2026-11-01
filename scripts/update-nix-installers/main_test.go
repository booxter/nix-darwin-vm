package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHash = "sha256-qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqo="

var digest = strings.Repeat("a", 64)

func testInstaller(version string) installer {
	return installer{
		URL:  "https://releases.nixos.org/nix/nix-" + version + "/nix-" + version + "-aarch64-darwin.tar.xz",
		Hash: testHash,
	}
}

type testTransport struct {
	handler http.Handler
}

func (transport testTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}

func releasePage(versions []string, marker string) string {
	var page strings.Builder
	page.WriteString("<ListBucketResult>")
	for _, version := range versions {
		fmt.Fprintf(&page, "<CommonPrefixes><Prefix>nix/nix-%s/</Prefix></CommonPrefixes>", version)
	}
	if marker != "" {
		fmt.Fprintf(&page, "<IsTruncated>true</IsTruncated><NextMarker>%s</NextMarker>", marker)
	}
	page.WriteString("</ListBucketResult>")
	return page.String()
}

func testClient(t *testing.T, pages map[string]string, serveArtifact http.HandlerFunc) *http.Client {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Host {
		case "nix-releases.s3.amazonaws.com":
			if r.Method != http.MethodGet || r.URL.Query().Get("prefix") != "nix/" || r.URL.Query().Get("delimiter") != "/" {
				t.Errorf("unexpected listing request: %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			page, ok := pages[r.URL.Query().Get("marker")]
			if !ok {
				t.Errorf("unexpected listing page: %s", r.URL)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, page)
		case "releases.nixos.org":
			serveArtifact(w, r)
		default:
			t.Errorf("unexpected host: %s", r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	// Exercise HTTP and redirect handling without opening sockets in the sandbox.
	client := newClient()
	client.Transport = testTransport{handler: handler}
	return client
}

func TestUpdateDiscoversInstallersAndPreservesHistory(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "nix.json")
	if err := writeInventory(filename, inventory{"nix": {"2.4": testInstaller("2.4")}}); err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{
		"":      releasePage([]string{"2.3", "2.10.0"}, "page2"),
		"page2": releasePage([]string{"2.35.4", "2.36pre1", "latest"}, ""),
	}
	verified := make(map[string]bool)
	client := testClient(t, pages, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/nix-2.3/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ".sha256") {
			verified[r.URL.String()] = true
			fmt.Fprintln(w, digest)
		} else if r.Method != http.MethodHead {
			t.Errorf("unexpected artifact request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	count, err := update(t.Context(), client, filename)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("got %d installers, want 3", count)
	}
	got, err := readInventory(filename)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"2.4", "2.10.0", "2.35.4"} {
		want := testInstaller(version)
		if got["nix"][version] != want || !verified[want.URL+".sha256"] {
			t.Fatalf("Nix %s was not pinned and verified: %+v", version, got)
		}
	}

	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update(t.Context(), client, filename); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("updating unchanged releases changed the inventory")
	}
}

func TestFailedUpdatePreservesInventory(t *testing.T) {
	tests := []struct {
		name           string
		checksum       string
		artifactStatus int
		checksumStatus int
		redirect       string
		changeURL      bool
		wantError      string
	}{
		{name: "changed checksum", checksum: strings.Repeat("b", 64), wantError: "differs from its committed record"},
		{name: "changed URL", checksum: digest, changeURL: true, wantError: "differs from its committed record"},
		{name: "invalid checksum", checksum: "invalid", wantError: "invalid SHA-256"},
		{name: "missing checksum", checksumStatus: http.StatusNotFound, wantError: "not found"},
		{name: "missing recorded installer", artifactStatus: http.StatusNotFound, wantError: "recorded installer is missing"},
		{name: "server failure", artifactStatus: http.StatusServiceUnavailable, wantError: "503 Service Unavailable"},
		{name: "redirect", artifactStatus: http.StatusFound, redirect: "https://untrusted.example/installer", wantError: "redirects are not allowed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := testInstaller("2.4")
			if test.changeURL {
				entry.URL = "https://untrusted.example/installer"
			}
			filename := filepath.Join(t.TempDir(), "nix.json")
			original, err := json.Marshal(inventory{"nix": {"2.4": entry}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, original, 0644); err != nil {
				t.Fatal(err)
			}
			client := testClient(t, map[string]string{"": releasePage([]string{"2.4"}, "")}, func(w http.ResponseWriter, r *http.Request) {
				status := test.artifactStatus
				if r.Method == http.MethodGet {
					status = test.checksumStatus
				}
				if test.redirect != "" {
					w.Header().Set("Location", test.redirect)
				}
				if status != 0 {
					w.WriteHeader(status)
					return
				}
				if r.Method == http.MethodGet {
					fmt.Fprint(w, test.checksum)
				}
			})

			_, err = update(t.Context(), client, filename)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want %q", err, test.wantError)
			}
			after, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, after) {
				t.Fatal("failed update changed the inventory")
			}
		})
	}
}

func TestInvalidListingsAreRejected(t *testing.T) {
	for _, page := range []string{
		"invalid XML",
		releasePage([]string{"2.36pre1"}, ""),
		"<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>",
	} {
		t.Run(page, func(t *testing.T) {
			client := testClient(t, map[string]string{"": page}, func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("invalid listing caused an artifact request: %s", r.URL)
			})
			filename := filepath.Join(t.TempDir(), "nix.json")
			if _, err := update(t.Context(), client, filename); err == nil {
				t.Fatal("invalid listing was accepted")
			}
			if _, err := os.Stat(filename); !os.IsNotExist(err) {
				t.Fatalf("invalid listing created an inventory: %v", err)
			}
		})
	}
}
