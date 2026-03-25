package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ImageManifest struct {
	Name    string `json:"name"`
	Tag     string `json:"tag"`
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

	fmt.Println("NAME\tTAG\tCREATED")

	for _, file := range files {

		data, err := os.ReadFile(filepath.Join(imagesPath, file.Name()))
		if err != nil {
			continue
		}

		var img ImageManifest
		json.Unmarshal(data, &img)

		fmt.Printf("%s\t%s\t%s\n", img.Name, img.Tag, img.Created)
	}
}

func RemoveImage(imageName string) {

	parts := strings.Split(imageName, ":")

	name := parts[0]
	tag := "latest"

	if len(parts) > 1 {
		tag = parts[1]
	}

	filename := name + "_" + tag + ".json"

	home, _ := os.UserHomeDir()
	imagePath := filepath.Join(home, ".docksmith", "images", filename)

	err := os.Remove(imagePath)

	if err != nil {
		fmt.Println("Image not found:", imageName)
		return
	}

	fmt.Println("Image removed:", imageName)
}

func BuildImage(tag string, context string) {

	docksmithfile := filepath.Join(context, "Docksmithfile")

	data, err := os.ReadFile(docksmithfile)

	if err != nil {
		fmt.Println("Docksmithfile not found")
		return
	}

	fmt.Println("Starting build...")
	fmt.Println("Image:", tag)
	fmt.Println("Context:", context)
	fmt.Println("Docksmithfile loaded")

	lines := strings.Split(string(data), "\n")

	for i, line := range lines {

		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		fmt.Printf("Step %d: %s\n", i+1, line)
	}

	parts := strings.Split(tag, ":")

	name := parts[0]
	imageTag := "latest"

	if len(parts) > 1 {
		imageTag = parts[1]
	}

	image := ImageManifest{
		Name:    name,
		Tag:     imageTag,
		Created: time.Now().Format(time.RFC3339),
	}

	manifestData, _ := json.MarshalIndent(image, "", "  ")

	home, _ := os.UserHomeDir()

	manifestPath := filepath.Join(
		home,
		".docksmith",
		"images",
		name+"_"+imageTag+".json",
	)

	os.WriteFile(manifestPath, manifestData, 0644)

	fmt.Println("Image built successfully:", tag)
}

func RunContainer(imageName string) {

	parts := strings.Split(imageName, ":")

	name := parts[0]
	tag := "latest"

	if len(parts) > 1 {
		tag = parts[1]
	}

	filename := name + "_" + tag + ".json"

	home, _ := os.UserHomeDir()
	imagePath := filepath.Join(home, ".docksmith", "images", filename)

	data, err := os.ReadFile(imagePath)

	if err != nil {
		fmt.Println("Image not found:", imageName)
		return
	}

	var img ImageManifest
	json.Unmarshal(data, &img)

	fmt.Println("Running container from image:", imageName)
	fmt.Println("Created:", img.Created)

	fmt.Println("Container started (simulation)")
	fmt.Println("Hello from Docksmith container!")
}
