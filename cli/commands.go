package cli

import (
	"docksmith/builder"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImageManifest is a minimal struct for listing / removing images.
// The full manifest lives in builder.ImageManifest — we only need a subset here.
type ImageManifest struct {
	Name    string `json:"name"`
	Tag     string `json:"tag"`
	Digest  string `json:"digest"`
	Created string `json:"created"`
}

func SetupDirectories() {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".docksmith")
	os.MkdirAll(filepath.Join(base, "images"), os.ModePerm)
	os.MkdirAll(filepath.Join(base, "layers"), os.ModePerm)
	os.MkdirAll(filepath.Join(base, "cache"), os.ModePerm)
}

func ListImages() {
	home, _ := os.UserHomeDir()
	imagesPath := filepath.Join(home, ".docksmith", "images")
	files, err := os.ReadDir(imagesPath)
	if err != nil {
		fmt.Println("Error reading images directory")
		return
	}
	if len(files) == 0 {
		fmt.Println("No images found")
		return
	}

	fmt.Printf("%-20s %-10s %-14s %s\n", "NAME", "TAG", "ID", "CREATED")
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(imagesPath, file.Name()))
		if err != nil {
			continue
		}
		var img ImageManifest
		json.Unmarshal(data, &img)
		id := img.Digest
		if len(id) > 19 { // "sha256:" = 7 chars + 12 hex chars
			id = id[7:19]
		}
		fmt.Printf("%-20s %-10s %-14s %s\n", img.Name, img.Tag, id, img.Created)
	}
}

func RemoveImage(imageName string) {
	parts := strings.Split(imageName, ":")
	name := parts[0]
	tag := "latest"
	if len(parts) > 1 {
		tag = parts[1]
	}

	home, _ := os.UserHomeDir()
	manifestPath := filepath.Join(home, ".docksmith", "images", name+"_"+tag+".json")

	// read manifest to find layer digests before removing
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Println("Image not found:", imageName)
		return
	}

	// parse to get layers
	var fullManifest struct {
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	json.Unmarshal(data, &fullManifest)

	// delete manifest
	os.Remove(manifestPath)

	// delete each layer file
	layersDir := filepath.Join(home, ".docksmith", "layers")
	for _, l := range fullManifest.Layers {
		hex := strings.TrimPrefix(l.Digest, "sha256:")
		layerPath := filepath.Join(layersDir, hex+".tar")
		os.Remove(layerPath) // ignore error — may already be gone
	}

	fmt.Println("Removed:", imageName)
}

// BuildImage is called by main.go for the `build` command.
func BuildImage(tag string, context string, noCache bool) {
	err := builder.Build(builder.BuildOptions{
		Tag:     tag,
		Context: context,
		NoCache: noCache,
	})
	if err != nil {
		fmt.Println("Build failed:", err)
		os.Exit(1)
	}
}

// RunContainer is called by main.go for the `run` command.
// Person 4 will implement the actual container execution inside runtime.RunContainer.
func RunContainer(imageName string, cmdOverride []string, envOverrides []string) {
	parts := strings.Split(imageName, ":")
	name := parts[0]
	tag := "latest"
	if len(parts) > 1 {
		tag = parts[1]
	}

	home, _ := os.UserHomeDir()
	imagePath := filepath.Join(home, ".docksmith", "images", name+"_"+tag+".json")
	data, err := os.ReadFile(imagePath)
	if err != nil {
		fmt.Println("Image not found:", imageName)
		os.Exit(1)
	}

	var manifest builder.ImageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		fmt.Println("Corrupt manifest:", err)
		os.Exit(1)
	}

	// determine command
	cmd := manifest.Config.Cmd
	if len(cmdOverride) > 0 {
		cmd = cmdOverride
	}
	if len(cmd) == 0 {
		fmt.Println("Error: no CMD defined and no command given")
		os.Exit(1)
	}

	// merge env: image env first, then -e overrides
	envMap := map[string]string{}
	for _, kv := range manifest.Config.Env {
		p := strings.SplitN(kv, "=", 2)
		if len(p) == 2 {
			envMap[p[0]] = p[1]
		}
	}
	for _, kv := range envOverrides {
		p := strings.SplitN(kv, "=", 2)
		if len(p) == 2 {
			envMap[p[0]] = p[1]
		}
	}

	// collect layer digests
	digests := make([]string, len(manifest.Layers))
	for i, l := range manifest.Layers {
		digests[i] = l.Digest
	}

	// assemble filesystem in a temp dir
	tmpDir, err := os.MkdirTemp("", "docksmith-run-*")
	if err != nil {
		fmt.Println("Failed to create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// extract layers (Person 3 provides this)
	// import is resolved at compile time — see layers package
	if err := extractLayersForRun(digests, tmpDir); err != nil {
		fmt.Println("Failed to assemble image:", err)
		os.Exit(1)
	}

	// build env slice
	envSlice := make([]string, 0, len(envMap))
	for k, v := range envMap {
		envSlice = append(envSlice, k+"="+v)
	}

	// run (Person 4 provides this)
	exitCode, err := runContainerForRun(tmpDir, cmd, manifest.Config.WorkingDir, envSlice)
	if err != nil {
		fmt.Println("Container error:", err)
	}
	fmt.Println("Exit code:", exitCode)
}
