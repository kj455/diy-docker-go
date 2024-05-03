//go:build linux
// +build linux

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
)

const (
	dockerAuthURL      = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/%s:pull" // repo
	dockerManifestsURL = "https://registry.hub.docker.com/v2/library/%s/manifests/%s"                               // repo, tag
	dockerBlobsURL     = "https://registry.hub.docker.com/v2/library/%s/blobs/%s"                                   // repo, digest
	HEADER_ACCEPT_API  = "application/vnd.docker.distribution.manifest.v2+json"
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
	endpoint := fmt.Sprintf(dockerAuthURL, d.name)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return fmt.Errorf("authorize: new request: %v", err)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		fmt.Printf("do request: %v", err)
		return err
	}
	defer resp.Body.Close()
	var tRes TokenResponse
	err = json.NewDecoder(resp.Body).Decode(&tRes)
	if err != nil {
		return fmt.Errorf("authorize: decode: %v", err)
	}
	d.token = tRes.Token
	return nil
}

func (d *DockerImageClient) getLayers() ([]Layer, error) {
	endpoint := fmt.Sprintf(dockerManifestsURL, d.name, d.tag)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("getLayers: new request: %v", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", d.token))
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getLayers: do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch layers: %v", resp.StatusCode)
	}
	var mRes ManifestListResponse
	err = json.NewDecoder(resp.Body).Decode(&mRes)
	if err != nil {
		return nil, fmt.Errorf("fetch layers decode: %v", err)
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
	var manifest *Manifest
	for _, m := range manifests {
		if m.Platform.Os == runtime.GOOS && m.Platform.Arch == runtime.GOARCH {
			manifest = &m
		}
	}
	if manifest == nil {
		return nil, fmt.Errorf("no manifest found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	endpoint := fmt.Sprintf(dockerManifestsURL, d.name, manifest.Digest)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch layers from manifests: %v", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", d.token))
	req.Header.Set("Accept", manifest.MediaType)
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch layers from manifests: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch layers from manifests: %v", resp.StatusCode)
	}
	var mRes ManifestListResponse
	err = json.NewDecoder(resp.Body).Decode(&mRes)
	if err != nil {
		return nil, fmt.Errorf("fetch layers from manifests decode: %v", err)
	}
	if len(mRes.Layers) == 0 {
		return nil, fmt.Errorf("no layers found in image manifest")
	}
	return mRes.Layers, nil
}

func (d *DockerImageClient) pullLayers(layers []Layer) error {
	for _, l := range layers {
		endpoint := fmt.Sprintf(dockerBlobsURL, d.name, l.Digest)
		req, err := http.NewRequest("GET", endpoint, nil)
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
		if err := d.saveLayer(l.Digest, resp.Body); err != nil {
			return fmt.Errorf("save layer: %v", err)
		}
	}
	return nil
}

func (d *DockerImageClient) saveLayer(name string, content io.Reader) error {
	fileName := fmt.Sprintf("%s.tar", name)
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
