package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var digest = strings.Repeat("a", 64)

const testHash = "sha256-qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqo="

func testFirmware(version, build string) firmware {
	return firmware{
		Version: version,
		Build:   build,
		URL:     "https://updates.cdn-apple.com/images/UniversalMac_" + version + "_" + build + "_Restore.ipsw",
		SHA256:  digest,
	}
}

func testImage(entry firmware) image {
	return image{Build: entry.Build, URL: entry.URL, Hash: testHash}
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

func testClient(t *testing.T, catalog []firmware, serveImage http.HandlerFunc) *http.Client {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "api.ipsw.me" && r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"firmwares": catalog})
			return
		}
		if r.Host != "updates.cdn-apple.com" || r.Method != http.MethodHead {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		serveImage(w, r)
	})

	// Keep HTTP fixtures inside the Nix sandbox without opening sockets.
	// Client.Do still applies the production redirect policy.
	client := newClient()
	client.Transport = testTransport{handler: handler}
	return client
}

func TestUpdatePreservesAndVerifiesHistory(t *testing.T) {
	older := testFirmware("26.6.2", "25G83")
	newer := testFirmware("27.2", "26B123")
	filename := filepath.Join(t.TempDir(), "macos.json")
	if err := writeInventory(filename, map[string]image{older.Version: testImage(older)}); err != nil {
		t.Fatal(err)
	}
	verified := make(map[string]bool)
	client := testClient(t, []firmware{newer}, func(w http.ResponseWriter, r *http.Request) {
		verified[r.URL.Path] = true
		w.Header().Set("x-amz-meta-digest-sha256", digest)
	})

	count, err := update(t.Context(), client, filename)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got %d images, want 2", count)
	}
	got, err := readInventory(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got[older.Version] != testImage(older) || got[newer.Version] != testImage(newer) {
		t.Fatalf("unexpected inventory: %+v", got)
	}
	if !verified["/images/UniversalMac_26.6.2_25G83_Restore.ipsw"] {
		t.Fatal("retained historical image was not verified against Apple")
	}
	if !verified["/images/UniversalMac_27.2_26B123_Restore.ipsw"] {
		t.Fatal("new image was not verified against Apple")
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
		t.Fatal("updating unchanged data changed the inventory")
	}
}

func TestVerificationFailurePreservesInventory(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		status    int
		redirect  string
		modify    func(*firmware)
		wantError string
	}{
		{
			name:      "hash mismatch",
			header:    strings.Repeat("b", 64),
			wantError: "SHA-256 mismatch",
		},
		{
			name:      "missing hash header",
			wantError: "Apple SHA-256 header",
		},
		{
			name:      "malformed hash header",
			header:    "not a digest",
			wantError: "Apple SHA-256 header",
		},
		{
			name:      "missing image",
			status:    http.StatusNotFound,
			wantError: "404 Not Found",
		},
		{
			name:      "redirect",
			status:    http.StatusFound,
			redirect:  "https://untrusted.example/image.ipsw",
			wantError: "redirects are not allowed",
		},
		{
			name: "untrusted image host",
			modify: func(entry *firmware) {
				entry.URL = strings.Replace(entry.URL, "updates.cdn-apple.com", "untrusted.example", 1)
			},
			wantError: "image must use HTTPS on updates.cdn-apple.com",
		},
		{
			name: "unencrypted image URL",
			modify: func(entry *firmware) {
				entry.URL = strings.Replace(entry.URL, "https:", "http:", 1)
			},
			wantError: "image must use HTTPS on updates.cdn-apple.com",
		},
		{
			name: "mislabeled image",
			modify: func(entry *firmware) {
				entry.Build = "wrong-build"
			},
			wantError: "filename does not match",
		},
		{
			name: "invalid catalog hash",
			modify: func(entry *firmware) {
				entry.SHA256 = "not a digest"
			},
			wantError: "invalid SHA-256 digest",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := testFirmware("27.2", "26B123")
			if test.modify != nil {
				test.modify(&entry)
			}
			filename := filepath.Join(t.TempDir(), "macos.json")
			original := []byte("{}\n")
			if err := os.WriteFile(filename, original, 0644); err != nil {
				t.Fatal(err)
			}
			client := testClient(t, []firmware{entry}, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("x-amz-meta-digest-sha256", test.header)
				if test.redirect != "" {
					w.Header().Set("Location", test.redirect)
				}
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
			})

			_, err := update(t.Context(), client, filename)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got error %v, want %q", err, test.wantError)
			}
			after, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, after) {
				t.Fatal("verification failure changed the inventory")
			}
		})
	}
}

func TestUpdateRejectsChangedPins(t *testing.T) {
	entry := testFirmware("27.2", "26B123")
	filename := filepath.Join(t.TempDir(), "macos.json")
	if err := writeInventory(filename, map[string]image{entry.Version: testImage(entry)}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	entry.SHA256 = strings.Repeat("b", 64)
	client := testClient(t, []firmware{entry}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-amz-meta-digest-sha256", entry.SHA256)
	})
	_, err = update(t.Context(), client, filename)
	if err == nil || !strings.Contains(err.Error(), "differs from its committed record") {
		t.Fatalf("unexpected error: %v", err)
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("changed pin overwrote the inventory")
	}
}
