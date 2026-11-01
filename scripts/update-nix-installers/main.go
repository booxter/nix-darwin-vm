package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	bucketURL   = "https://nix-releases.s3.amazonaws.com/"
	releasesURL = "https://releases.nixos.org/"
)

var (
	stableVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)
	errNotFound   = errors.New("not found")
)

type installer struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

type inventory map[string]map[string]installer

func newClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are not allowed")
		},
	}
}

func request(ctx context.Context, client *http.Client, method, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s %s: %w", method, address, errNotFound)
	}
	return nil, fmt.Errorf("%s %s: %s", method, address, response.Status)
}

func discover(ctx context.Context, client *http.Client) (map[string]bool, error) {
	versions := make(map[string]bool)
	marker := ""
	for {
		query := url.Values{"delimiter": {"/"}, "prefix": {"nix/"}}
		if marker != "" {
			query.Set("marker", marker)
		}
		response, err := request(ctx, client, http.MethodGet, bucketURL+"?"+query.Encode())
		if err != nil {
			return nil, err
		}
		var page struct {
			Prefixes    []string `xml:"CommonPrefixes>Prefix"`
			IsTruncated bool
			NextMarker  string
		}
		err = xml.NewDecoder(response.Body).Decode(&page)
		response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode release listing: %w", err)
		}

		for _, prefix := range page.Prefixes {
			version, ok := strings.CutPrefix(prefix, "nix/nix-")
			if !ok || !strings.HasSuffix(version, "/") {
				continue
			}
			version = strings.TrimSuffix(version, "/")
			if stableVersion.MatchString(version) {
				versions[version] = true
			}
		}
		if !page.IsTruncated {
			break
		}
		if page.NextMarker <= marker {
			return nil, errors.New("release listing has no advancing pagination marker")
		}
		marker = page.NextMarker
	}
	if len(versions) == 0 {
		return nil, errors.New("release listing contains no stable versions")
	}
	return versions, nil
}

func readInstaller(ctx context.Context, client *http.Client, version string) (*installer, error) {
	address := releasesURL + "nix/nix-" + version + "/nix-" + version + "-aarch64-darwin.tar.xz"
	response, err := request(ctx, client, http.MethodHead, address)
	if errors.Is(err, errNotFound) {
		// Older Nix releases may not have an ARM Darwin binary distribution.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	response.Body.Close()

	response, err = request(ctx, client, http.MethodGet, address+".sha256")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	digest, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(digest)))
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("invalid SHA-256 checksum for %s", address)
	}
	return &installer{URL: address, Hash: "sha256-" + base64.StdEncoding.EncodeToString(decoded)}, nil
}

func readInventory(filename string) (inventory, error) {
	entries := make(inventory)
	data, err := os.ReadFile(filename)
	if err == nil {
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("read inventory: %w", err)
		}
		if entries == nil {
			return nil, errors.New("inventory must be a JSON object")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if entries["nix"] == nil {
		entries["nix"] = make(map[string]installer)
	}
	return entries, nil
}

func writeInventory(filename string, entries inventory) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".nix-installers-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := temporary.Chmod(0644); err != nil {
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), filename)
}

func update(ctx context.Context, client *http.Client, filename string) (int, error) {
	entries, err := readInventory(filename)
	if err != nil {
		return 0, err
	}
	versions, err := discover(ctx, client)
	if err != nil {
		return 0, err
	}
	for version := range entries["nix"] {
		if !stableVersion.MatchString(version) {
			return 0, fmt.Errorf("invalid recorded version %q", version)
		}
		versions[version] = true
	}

	for _, version := range slices.Sorted(maps.Keys(versions)) {
		candidate, err := readInstaller(ctx, client, version)
		if err != nil {
			return 0, fmt.Errorf("Nix %s: %w", version, err)
		}
		previous, recorded := entries["nix"][version]
		if candidate == nil {
			if recorded {
				return 0, fmt.Errorf("Nix %s: recorded installer is missing", version)
			}
			continue
		}
		if recorded && previous != *candidate {
			return 0, fmt.Errorf("Nix %s differs from its committed record; review it manually", version)
		}
		entries["nix"][version] = *candidate
	}
	if len(entries["nix"]) == 0 {
		return 0, errors.New("no ARM Darwin installers found")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := writeInventory(filename, entries); err != nil {
		return 0, err
	}
	return len(entries["nix"]), nil
}

func main() {
	output := flag.String("output", "installers/nix.json", "inventory file to update")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "update-nix-installers: unexpected positional arguments")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	count, err := update(ctx, newClient(), *output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "update-nix-installers:", err)
		os.Exit(1)
	}
	fmt.Printf("Verified %d Nix installers; updated %s\n", count, *output)
}
