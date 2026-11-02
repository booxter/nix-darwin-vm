package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

const catalogURL = "https://api.ipsw.me/v4/device/VirtualMac2,1?type=ipsw"

type image struct {
	Build string `json:"build"`
	URL   string `json:"url"`
	Hash  string `json:"hash"`
}

type firmware struct {
	Version string `json:"version"`
	Build   string `json:"buildid"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256sum"`
}

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
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("%s %s: %s", method, address, response.Status)
	}
	return response, nil
}

func nixHash(digest string) (string, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("invalid SHA-256 digest %q", digest)
	}
	return "sha256-" + base64.StdEncoding.EncodeToString(decoded), nil
}

func discover(ctx context.Context, client *http.Client) (map[string]image, error) {
	response, err := request(ctx, client, http.MethodGet, catalogURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var catalog struct {
		Firmwares []firmware `json:"firmwares"`
	}
	if err := json.NewDecoder(response.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if len(catalog.Firmwares) == 0 {
		return nil, errors.New("catalog contains no images")
	}

	images := make(map[string]image)
	for _, entry := range catalog.Firmwares {
		if entry.Version == "" || entry.Build == "" {
			return nil, errors.New("catalog image is missing its version or build")
		}
		hash, err := nixHash(entry.SHA256)
		if err != nil {
			return nil, fmt.Errorf("macOS %s: %w", entry.Version, err)
		}
		candidate := image{Build: entry.Build, URL: entry.URL, Hash: hash}
		if previous, exists := images[entry.Version]; exists && previous != candidate {
			return nil, fmt.Errorf("conflicting catalog entries for macOS %s", entry.Version)
		}
		images[entry.Version] = candidate
	}
	return images, nil
}

func readInventory(filename string) (map[string]image, error) {
	images := make(map[string]image)
	data, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return images, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &images); err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	if images == nil {
		return nil, errors.New("inventory must be a JSON object")
	}
	return images, nil
}

func verifyImage(ctx context.Context, client *http.Client, version string, entry image) error {
	address, err := url.Parse(entry.URL)
	if err != nil {
		return err
	}
	if address.Scheme != "https" || address.Host != "updates.cdn-apple.com" || address.User != nil {
		return fmt.Errorf("image must use HTTPS on updates.cdn-apple.com: %s", entry.URL)
	}
	expectedName := fmt.Sprintf("UniversalMac_%s_%s_Restore.ipsw", version, entry.Build)
	if path.Base(address.Path) != expectedName {
		return errors.New("image filename does not match its version and build")
	}

	response, err := request(ctx, client, http.MethodHead, entry.URL)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	hash, err := nixHash(response.Header.Get("x-amz-meta-digest-sha256"))
	if err != nil {
		return fmt.Errorf("Apple SHA-256 header: %w", err)
	}
	if hash != entry.Hash {
		return fmt.Errorf("SHA-256 mismatch: inventory has %s, Apple reports %s", entry.Hash, hash)
	}
	return nil
}

func writeInventory(filename string, images map[string]image) error {
	data, err := json.MarshalIndent(images, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".macos-images-*.json")
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
	images, err := readInventory(filename)
	if err != nil {
		return 0, err
	}
	discovered, err := discover(ctx, client)
	if err != nil {
		return 0, err
	}
	for version, candidate := range discovered {
		if previous, exists := images[version]; exists && previous != candidate {
			return 0, fmt.Errorf("macOS %s differs from its committed record; review it manually", version)
		}
		images[version] = candidate
	}

	for _, version := range slices.Sorted(maps.Keys(images)) {
		if err := verifyImage(ctx, client, version, images[version]); err != nil {
			return 0, fmt.Errorf("macOS %s: %w", version, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := writeInventory(filename, images); err != nil {
		return 0, err
	}
	return len(images), nil
}

func main() {
	output := flag.String("output", "images/macos.json", "inventory file to update")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "update-images: unexpected positional arguments")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	count, err := update(ctx, newClient(), *output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "update-images:", err)
		os.Exit(1)
	}
	fmt.Printf("Verified %d images against Apple; updated %s\n", count, *output)
}
