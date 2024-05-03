//go:build linux
// +build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"

	"golang.org/x/sync/errgroup"
)

const (
	dockerAuthURL      = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/%s:pull" // repo
	dockerManifestsURL = "https://registry.hub.docker.com/v2/library/%s/manifests/%s"                               // repo, tag
	dockerBlobsURL     = "https://registry.hub.docker.com/v2/library/%s/blobs/%s"                                   // repo, digest
	layerFileName      = "%s.tar"
)

type DockerImageClient struct {
	http  *http.Client
	name  string
	tag   string
	token string
	dir   string
}

func newDockerImageClient(name, dir string) *DockerImageClient {
	parts := strings.Split(name, ":")
	var nam, tag string
	if len(parts) == 1 {
		nam = parts[0]
		tag = "latest"
	}
	return &DockerImageClient{
		http: &http.Client{},
		name: nam,
		tag:  tag,
		dir:  dir,
	}
}

type TokenResponse struct {
	Token string `json:"token"`
}

type Manifest struct {
	Platform  Platform `json:"platform"`
	Digest    string   `json:"digest"`
	MediaType string   `json:"mediaType"`
}

type Platform struct {
	Arch string `json:"architecture"`
	Os   string `json:"os"`
}

type Layer struct {
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
	Digest    string `json:"digest"`
}

type ManifestListResponse struct {
	Manifests []Manifest `json:"manifests"`
	Layers    []Layer    `json:"layers"`
}

func (d *DockerImageClient) Pull() error {
	if err := d.authorize(); err != nil {
		return err
	}
	layers, err := d.getLayers()
	if err != nil {
		return err
	}
	return d.pullLayers(layers)
}

func (d *DockerImageClient) authorize() error {
	url := fmt.Sprintf(dockerAuthURL, d.name)	
	var tokenRes TokenResponse
	if err := doGet(d.http, url, nil, &tokenRes); err != nil {
		return fmt.Errorf("authorize: %v", err)
	}
	d.token = tokenRes.Token
	return nil
}

func (d *DockerImageClient) getLayers() ([]Layer, error) {
	url := fmt.Sprintf(dockerManifestsURL, d.name, d.tag)
	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", d.token),
		"Accept":        "application/vnd.docker.distribution.manifest.v2+json",
	}
	var mRes ManifestListResponse
	if err := doGet(d.http, url, headers, &mRes); err != nil {
		return nil, fmt.Errorf("get layers: %v", err)
	}
	if len(mRes.Manifests) > 0 {
		ms, err := d.getLayersFromManifests(mRes.Manifests)
		if err != nil {
			return nil, err
		}
		return ms, nil
	}
	if len(mRes.Layers) == 0 {
		return nil, fmt.Errorf("no layers found in manifest")
	}
	return mRes.Layers, nil
}

func (d *DockerImageClient) getLayersFromManifests(manifests []Manifest) ([]Layer, error) {
	manifest, err := findArchMatchingManifest(manifests)
	if err != nil {
		return nil, fmt.Errorf("no manifest found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	url := fmt.Sprintf(dockerManifestsURL, d.name, manifest.Digest)
	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", d.token),
		"Accept":        "application/vnd.docker.distribution.manifest.v2+json",
	}
	var mRes ManifestListResponse
	if err := doGet(d.http, url, headers, &mRes); err != nil {
		return nil, fmt.Errorf("get layers from manifests: %v", err)
	}
	if len(mRes.Layers) == 0 {
		return nil, fmt.Errorf("no layers found in image manifest")
	}
	return mRes.Layers, nil
}

func findArchMatchingManifest(manifests []Manifest) (*Manifest, error) {
	for _, m := range manifests {
		if m.Platform.Os == runtime.GOOS && m.Platform.Arch == runtime.GOARCH {
			return &m, nil
		}
	}
	return nil, fmt.Errorf("no matching manifest found")
}

func (d *DockerImageClient) pullLayers(layers []Layer) error {
	eg, ctx := errgroup.WithContext(context.Background())
	for _, layer := range layers {
		eg.Go(func() error {
			select {
			case <-ctx.Done():
				return nil
			default:
				url := fmt.Sprintf(dockerBlobsURL, d.name, layer.Digest)
				req, err := http.NewRequest("GET", url, nil)
				if err != nil {
					return fmt.Errorf("pull layers: %v", err)
				}
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", d.token))
				resp, err := d.http.Do(req)
				if err != nil {
					return fmt.Errorf("pull layers: %v", err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("pull layers: %v", resp.StatusCode)
				}
				if err := d.saveLayer(layer.Digest, resp.Body); err != nil {
					return fmt.Errorf("save layer: %v", err)
				}
				return nil
			}
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	return nil
}

func (d *DockerImageClient) saveLayer(name string, content io.Reader) error {
	fileName := fmt.Sprintf(layerFileName, name)
	filePath := path.Join(d.dir, fileName)
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("create file: %v", err)
	}
	defer file.Close()
	fileWriter := bufio.NewWriter(file)
	if _, err = io.Copy(fileWriter, content); err != nil {
		return fmt.Errorf("copy file: %v", err)
	}
	return d.extractLayer(filePath)
}

func (d *DockerImageClient) extractLayer(fileName string) error {
	cmd := exec.Command("tar", "xvvf", fileName, "-C", d.dir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error while running tar command: %v", err)
	}
	return os.Remove(fileName)
}

func doGet[T any](client *http.Client, url string, headers map[string]string, res *T) (error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("do request: %v", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(res); err != nil {
		return fmt.Errorf("decode: %v", err)
	}
	return nil
}
