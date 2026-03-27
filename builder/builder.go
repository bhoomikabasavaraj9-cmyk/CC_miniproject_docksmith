package builder

import (
	"crypto/sha256"
	"docksmith/cache"
	"docksmith/layers"
	"docksmith/parser"
	"docksmith/runtime"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LayerEntry mirrors what goes into the manifest's layers array
type LayerEntry struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	CreatedBy string `json:"createdBy"`
}

// ImageConfig holds the image-level config stored in the manifest
type ImageConfig struct {
	Env        []string `json:"Env"`
	Cmd        []string `json:"Cmd"`
	WorkingDir string   `json:"WorkingDir"`
}

// ImageManifest is the full manifest written to ~/.docksmith/images/
type ImageManifest struct {
	Name    string       `json:"name"`
	Tag     string       `json:"tag"`
	Digest  string       `json:"digest"`
	Created string       `json:"created"`
	Config  ImageConfig  `json:"config"`
	Layers  []LayerEntry `json:"layers"`
}

// BuildOptions controls build behaviour
type BuildOptions struct {
	Tag     string // "name:tag"
	Context string // path to build context directory
	NoCache bool
}

// Build runs the full build pipeline for a Docksmithfile.
func Build(opts BuildOptions) error {
	// ── 1. Parse Docksmithfile ──────────────────────────────────────────────
	docksmithfilePath := filepath.Join(opts.Context, "Docksmithfile")
	instructions, err := parser.ParseFile(docksmithfilePath)
	if err != nil {
		return err
	}

	// ── 2. Split name and tag ───────────────────────────────────────────────
	parts := strings.SplitN(opts.Tag, ":", 2)
	imageName := parts[0]
	imageTag := "latest"
	if len(parts) == 2 {
		imageTag = parts[1]
	}

	// ── 3. State accumulated during the build ──────────────────────────────
	var (
		collectedLayers []LayerEntry
		envMap          = map[string]string{} // key → value
		workdir         = ""
		cmdSlice        []string
		
		lastLayerDigest = "" // digest of the last COPY/RUN layer (or base manifest)
		anyCacheMiss    = false
		totalStart      = time.Now()
		stepIndex       = 0 // displayed step number (1-based)
	)

	// ── 4. Walk instructions ────────────────────────────────────────────────
	for _, instr := range instructions {
		stepIndex++

		switch instr.Type {

		// ── FROM ────────────────────────────────────────────────────────────
		case parser.FROM:
			fmt.Printf("Step %d/%d : FROM %s\n", stepIndex, len(instructions), instr.Args)

			baseManifest, err := loadBaseImage(instr.Args)
			if err != nil {
				return fmt.Errorf("FROM: %w", err)
			}

			// inherit base layers
			collectedLayers = append(collectedLayers, baseManifest.Layers...)

			// inherit base config as defaults (will be overwritten by later instructions)
			for _, kv := range baseManifest.Config.Env {
				p := strings.SplitN(kv, "=", 2)
				if len(p) == 2 {
					envMap[p[0]] = p[1]
				}
			}
			if baseManifest.Config.WorkingDir != "" {
				workdir = baseManifest.Config.WorkingDir
			}
			cmdSlice = baseManifest.Config.Cmd

			lastLayerDigest = baseManifest.Digest

		// ── WORKDIR ─────────────────────────────────────────────────────────
		case parser.WORKDIR:
			workdir = instr.Args
			fmt.Printf("Step %d/%d : WORKDIR %s\n", stepIndex, len(instructions), instr.Args)

		// ── ENV ─────────────────────────────────────────────────────────────
		case parser.ENV:
			p := strings.SplitN(instr.Args, "=", 2)
			if len(p) != 2 {
				return fmt.Errorf("line %d: ENV must be KEY=VALUE, got: %s", instr.LineNum, instr.Args)
			}
			envMap[p[0]] = p[1]
			fmt.Printf("Step %d/%d : ENV %s\n", stepIndex, len(instructions), instr.Args)

		// ── CMD ─────────────────────────────────────────────────────────────
		case parser.CMD:
			parsed, err := parseJSONArray(instr.Args)
			if err != nil {
				return fmt.Errorf("line %d: CMD must be a JSON array: %w", instr.LineNum, err)
			}
			cmdSlice = parsed
			fmt.Printf("Step %d/%d : CMD %s\n", stepIndex, len(instructions), instr.Args)

		// ── COPY ────────────────────────────────────────────────────────────
		case parser.COPY:
			stepStart := time.Now()
			cacheKey, err := computeCacheKey(lastLayerDigest, instr, workdir, envMap, opts.Context)
			if err != nil {
				return fmt.Errorf("COPY cache key: %w", err)
			}

			hit, layerDigest := tryCache(cacheKey, opts.NoCache, anyCacheMiss)
			if hit {
				fmt.Printf("Step %d/%d : COPY %s [CACHE HIT]\n", stepIndex, len(instructions), instr.Args)
				entry, err := layerEntryFromDigest(layerDigest, "COPY "+instr.Args)
				if err != nil {
					return err
				}
				collectedLayers = append(collectedLayers, entry)
				lastLayerDigest = layerDigest
			} else {
				anyCacheMiss = true
				// build the assembled root so COPY can write into it
				assembledRoot, cleanup, err := assembleLayers(collectedLayers)
				if err != nil {
					return err
				}

				// ensure WORKDIR exists inside the assembled root
				if workdir != "" {
					os.MkdirAll(filepath.Join(assembledRoot, workdir), 0755)
				}

				layerDigest, err = layers.CreateCopyLayer(opts.Context, assembledRoot, instr.Args)
				cleanup()
				if err != nil {
					return fmt.Errorf("COPY failed: %w", err)
				}

				if !opts.NoCache {
					cache.Store(cacheKey, layerDigest)
				}

				elapsed := time.Since(stepStart).Seconds()
				fmt.Printf("Step %d/%d : COPY %s [CACHE MISS] %.2fs\n",
					stepIndex, len(instructions), instr.Args, elapsed)

				entry, err := layerEntryFromDigest(layerDigest, "COPY "+instr.Args)
				if err != nil {
					return err
				}
				collectedLayers = append(collectedLayers, entry)
				lastLayerDigest = layerDigest
			}

		// ── RUN ─────────────────────────────────────────────────────────────
		case parser.RUN:
			stepStart := time.Now()
			cacheKey, err := computeCacheKey(lastLayerDigest, instr, workdir, envMap, "")
			if err != nil {
				return fmt.Errorf("RUN cache key: %w", err)
			}

			hit, layerDigest := tryCache(cacheKey, opts.NoCache, anyCacheMiss)
			if hit {
				fmt.Printf("Step %d/%d : RUN %s [CACHE HIT]\n", stepIndex, len(instructions), instr.Args)
				entry, err := layerEntryFromDigest(layerDigest, "RUN "+instr.Args)
				if err != nil {
					return err
				}
				collectedLayers = append(collectedLayers, entry)
				lastLayerDigest = layerDigest
			} else {
				anyCacheMiss = true
				// assemble filesystem for isolation
				assembledRoot, cleanup, err := assembleLayers(collectedLayers)
				if err != nil {
					return err
				}

				// ensure WORKDIR exists
				if workdir != "" {
					os.MkdirAll(filepath.Join(assembledRoot, workdir), 0755)
				}

				// run inside isolated container (same primitive as docksmith run)
				envSlice := envMapToSlice(envMap)
				layerDigest, err = runtime.RunInContainer(assembledRoot, instr.Args, workdir, envSlice)
				cleanup()
				if err != nil {
					return fmt.Errorf("RUN failed: %w", err)
				}

				if !opts.NoCache {
					cache.Store(cacheKey, layerDigest)
				}

				elapsed := time.Since(stepStart).Seconds()
				fmt.Printf("Step %d/%d : RUN %s [CACHE MISS] %.2fs\n",
					stepIndex, len(instructions), instr.Args, elapsed)

				entry, err := layerEntryFromDigest(layerDigest, "RUN "+instr.Args)
				if err != nil {
					return err
				}
				collectedLayers = append(collectedLayers, entry)
				lastLayerDigest = layerDigest
			}

		default:
			return fmt.Errorf("line %d: unhandled instruction %s", instr.LineNum, instr.Type)
		}
	}

	// ── 5. Build final image config ─────────────────────────────────────────
	finalEnv := envMapToSlice(envMap)
	sort.Strings(finalEnv) // consistent ordering

	config := ImageConfig{
		Env:        finalEnv,
		Cmd:        cmdSlice,
		WorkingDir: workdir,
	}

	// ── 6. Determine created timestamp ─────────────────────────────────────
	// If all steps were cache hits AND the old manifest exists, preserve its timestamp.
	createdAt := time.Now().Format(time.RFC3339)
	if !anyCacheMiss {
		if old, err := loadExistingManifest(imageName, imageTag); err == nil {
			createdAt = old.Created
		}
	}

	// ── 7. Write manifest ───────────────────────────────────────────────────
	manifest := ImageManifest{
		Name:    imageName,
		Tag:     imageTag,
		Digest:  "", // filled in after digest computation
		Created: createdAt,
		Config:  config,
		Layers:  collectedLayers,
	}

	digest, err := computeManifestDigest(manifest)
	if err != nil {
		return fmt.Errorf("manifest digest: %w", err)
	}
	manifest.Digest = "sha256:" + digest

	if err := saveManifest(manifest); err != nil {
		return fmt.Errorf("saving manifest: %w", err)
	}

	totalElapsed := time.Since(totalStart).Seconds()
	fmt.Printf("\nSuccessfully built %s %s:%s (%.2fs)\n",
		manifest.Digest[:19], imageName, imageTag, totalElapsed)

	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// loadBaseImage reads the image manifest for a base image (e.g. "alpine:3.18")
func loadBaseImage(imageRef string) (*ImageManifest, error) {
	parts := strings.SplitN(imageRef, ":", 2)
	name := parts[0]
	tag := "latest"
	if len(parts) == 2 {
		tag = parts[1]
	}

	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".docksmith", "images", name+"_"+tag+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("base image %q not found in local store (run setup first)", imageRef)
	}

	var m ImageManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("corrupt manifest for %q: %w", imageRef, err)
	}
	return &m, nil
}

// loadExistingManifest reads an already-built manifest for a given name:tag
func loadExistingManifest(name, tag string) (*ImageManifest, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".docksmith", "images", name+"_"+tag+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m ImageManifest
	return &m, json.Unmarshal(data, &m)
}

// saveManifest writes the manifest JSON to ~/.docksmith/images/
func saveManifest(m ImageManifest) error {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".docksmith", "images", m.Name+"_"+m.Tag+".json")
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// computeManifestDigest serialises the manifest with Digest="" and returns the sha256 hex string
func computeManifestDigest(m ImageManifest) (string, error) {
	m.Digest = ""
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

// computeCacheKey produces the deterministic cache key for a COPY or RUN instruction.
func computeCacheKey(prevDigest string, instr parser.Instruction, workdir string, envMap map[string]string, contextDir string) (string, error) {
	h := sha256.New()

	// 1. previous layer digest
	h.Write([]byte(prevDigest))

	// 2. full instruction text
	h.Write([]byte(string(instr.Type) + " " + instr.Args))

	// 3. WORKDIR value
	h.Write([]byte(workdir))

	// 4. ENV state, sorted by key
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k + "=" + envMap[k] + "\n"))
	}

	// 5. COPY only: hash of each source file in sorted order
	if instr.Type == parser.COPY {
		srcHash, err := layers.HashCopySources(contextDir, instr.Args)
		if err != nil {
			return "", err
		}
		h.Write([]byte(srcHash))
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// tryCache checks the cache and returns (hit bool, layerDigest string).
// Returns false immediately if noCache is true or anyCacheMiss is true (cascade).
func tryCache(key string, noCache bool, anyCacheMiss bool) (bool, string) {
	if noCache || anyCacheMiss {
		return false, ""
	}
	digest, ok := cache.Lookup(key)
	if !ok {
		return false, ""
	}
	// verify layer file actually exists on disk
	if !layers.LayerExists(digest) {
		return false, ""
	}
	return true, digest
}

// assembleLayers extracts all collected layer tars into a fresh temp directory.
// Returns the directory path, a cleanup func, and any error.
func assembleLayers(collected []LayerEntry) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "docksmith-build-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	digests := make([]string, len(collected))
	for i, l := range collected {
		digests[i] = l.Digest
	}
	if err := layers.ExtractLayers(digests, tmpDir); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmpDir, cleanup, nil
}

// layerEntryFromDigest builds a LayerEntry by reading the tar file size from disk.
func layerEntryFromDigest(digest, createdBy string) (LayerEntry, error) {
	home, _ := os.UserHomeDir()
	// digest is "sha256:abc123..." — strip the prefix for the filename
	hexDigest := strings.TrimPrefix(digest, "sha256:")
	path := filepath.Join(home, ".docksmith", "layers", hexDigest+".tar")
	info, err := os.Stat(path)
	if err != nil {
		return LayerEntry{}, fmt.Errorf("layer file not found for digest %s: %w", digest, err)
	}
	return LayerEntry{
		Digest:    digest,
		Size:      info.Size(),
		CreatedBy: createdBy,
	}, nil
}

// envMapToSlice converts map → ["KEY=value", ...] sorted by key
func envMapToSlice(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(m))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

// parseJSONArray parses a CMD argument like ["echo","hello"] into []string
func parseJSONArray(s string) ([]string, error) {
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, err
	}
	return arr, nil
}
