package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	ferrouswheel "m31labs.dev/ferrous-wheel"
)

const usagePackage = "Usage: ferrous-wheel package --compiler-sha256 HEX --go-version goX.Y.Z --go-sha256 HEX --target OS/ARCH [--target OS/ARCH] --out DIR <file.fw>\n"

var goReleaseVersion = regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`)

type packageTarget struct {
	GOOS   string
	GOARCH string
}

type packageTargets []packageTarget

func (targets *packageTargets) String() string {
	parts := make([]string, len(*targets))
	for i, target := range *targets {
		parts[i] = target.GOOS + "/" + target.GOARCH
	}
	return strings.Join(parts, ",")
}

func (targets *packageTargets) Set(value string) error {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !supportedPackageTarget(parts[0], parts[1]) {
		return fmt.Errorf("unsupported target %q; use linux, darwin, or windows with amd64 or arm64", value)
	}
	for _, target := range *targets {
		if target.GOOS == parts[0] && target.GOARCH == parts[1] {
			return fmt.Errorf("duplicate target %q", value)
		}
	}
	*targets = append(*targets, packageTarget{GOOS: parts[0], GOARCH: parts[1]})
	return nil
}

func supportedPackageTarget(goos, goarch string) bool {
	switch goos {
	case "linux", "darwin", "windows":
		return goarch == "amd64" || goarch == "arm64"
	default:
		return false
	}
}

type packageOptions struct {
	CompilerSHA256 string
	GoVersion      string
	GoSHA256       string
	GoBinary       string
	OutputDir      string
	Targets        packageTargets
	SourcePath     string
}

type packageArtifact struct {
	File       string `json:"file"`
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	CGOEnabled bool   `json:"cgoEnabled"`
	Static     bool   `json:"static"`
}

type packageManifest struct {
	SchemaVersion   int               `json:"schemaVersion"`
	CompilerSHA256  string            `json:"compilerSHA256"`
	GoVersion       string            `json:"goVersion"`
	GoBinarySHA256  string            `json:"goBinarySHA256"`
	SourceFile      string            `json:"sourceFile"`
	SourceSHA256    string            `json:"sourceSHA256"`
	ModuleSHA256    string            `json:"moduleSHA256,omitempty"`
	ModuleSumSHA256 string            `json:"moduleSumSHA256,omitempty"`
	Artifacts       []packageArtifact `json:"artifacts"`
}

func parsePackageArgs(args []string) (packageOptions, error) {
	var options packageOptions
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.CompilerSHA256, "compiler-sha256", "", "exact compiler binary digest")
	flags.StringVar(&options.GoVersion, "go-version", "", "exact Go release version")
	flags.StringVar(&options.GoSHA256, "go-sha256", "", "exact Go executable digest")
	flags.StringVar(&options.GoBinary, "go", "go", "Go toolchain executable")
	flags.StringVar(&options.OutputDir, "out", "", "new output directory")
	flags.Var(&options.Targets, "target", "target OS/ARCH")
	if err := flags.Parse(args); err != nil {
		return options, fmt.Errorf("%w\n%s", err, usagePackage)
	}
	if flags.NArg() != 1 || options.OutputDir == "" || len(options.Targets) == 0 ||
		!strings.HasSuffix(flags.Arg(0), ".fw") || filepath.Base(flags.Arg(0)) == ".fw" {
		return options, errors.New(usagePackage)
	}
	options.SourcePath = flags.Arg(0)
	if err := validatePackageHash("--compiler-sha256", options.CompilerSHA256); err != nil {
		return options, err
	}
	if err := validatePackageHash("--go-sha256", options.GoSHA256); err != nil {
		return options, err
	}
	options.CompilerSHA256 = strings.ToLower(options.CompilerSHA256)
	options.GoSHA256 = strings.ToLower(options.GoSHA256)
	if !goReleaseVersion.MatchString(options.GoVersion) {
		return options, errors.New("package: --go-version must name an exact Go release, such as go1.25.1")
	}
	sort.Slice(options.Targets, func(i, j int) bool {
		a, b := options.Targets[i], options.Targets[j]
		return a.GOOS+"/"+a.GOARCH < b.GOOS+"/"+b.GOARCH
	})
	return options, nil
}

func validatePackageHash(flagName, value string) error {
	if len(value) != 64 {
		return fmt.Errorf("package: %s must be 64 hexadecimal characters", flagName)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("package: %s must be hexadecimal", flagName)
	}
	return nil
}

func runPackageCLI(args []string, stderr io.Writer) int {
	options, err := parsePackageArgs(args)
	if err == nil {
		err = packageSource(options)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func packageSource(options packageOptions) (returnErr error) {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find compiler executable: %w", err)
	}
	compilerHash, err := fileSHA256(executable)
	if err != nil {
		return fmt.Errorf("hash compiler executable: %w", err)
	}
	if compilerHash != options.CompilerSHA256 {
		return fmt.Errorf("compiler SHA-256 mismatch: got %s", compilerHash)
	}
	goBinary, err := exec.LookPath(options.GoBinary)
	if err != nil {
		return fmt.Errorf("find Go toolchain: %w", err)
	}
	goBinary, err = filepath.Abs(goBinary)
	if err != nil {
		return fmt.Errorf("resolve Go toolchain: %w", err)
	}
	goHash, err := fileSHA256(goBinary)
	if err != nil {
		return fmt.Errorf("hash Go toolchain: %w", err)
	}
	if goHash != options.GoSHA256 {
		return fmt.Errorf("Go executable SHA-256 mismatch: got %s", goHash)
	}
	versionCmd := exec.Command(goBinary, "version")
	versionCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOENV=off")
	versionOutput, err := versionCmd.Output()
	if err != nil {
		return fmt.Errorf("read Go toolchain version: %w", err)
	}
	fields := strings.Fields(string(versionOutput))
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "version" || fields[2] != options.GoVersion {
		return fmt.Errorf("Go version mismatch: requested %s, got %q", options.GoVersion, strings.TrimSpace(string(versionOutput)))
	}

	source, err := os.ReadFile(options.SourcePath)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	if lintFile(options.SourcePath, source, os.Stderr) {
		return errors.New("package: source has lint errors")
	}
	goCode, warnings, err := ferrouswheel.TranspileWithOptions(source, ferrouswheel.TranspileOptions{
		SourceFile: filepath.Base(options.SourcePath),
		LintRan:    true,
	})
	if err != nil {
		return fmt.Errorf("transpile source: %w", err)
	}
	printWarnings(os.Stderr, warnings)
	tmpGoDir, cleanupGo, err := writeTempProject(generatedHeader+goCode, options.SourcePath)
	if err != nil {
		return err
	}
	defer cleanupGo()

	outputDir, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	if _, err := os.Lstat(outputDir); err == nil {
		return fmt.Errorf("package output already exists: %s", outputDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect package output: %w", err)
	}
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fmt.Errorf("create package parent directory: %w", err)
	}
	stageDir, err := os.MkdirTemp(parent, ".ferrous-package-*")
	if err != nil {
		return fmt.Errorf("create package staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stageDir); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove package staging directory: %w", err))
		}
	}()

	manifest := packageManifest{
		SchemaVersion:  1,
		CompilerSHA256: compilerHash,
		GoVersion:      options.GoVersion,
		GoBinarySHA256: goHash,
		SourceFile:     filepath.Base(options.SourcePath),
		SourceSHA256:   bytesSHA256(source),
	}
	absSource, err := filepath.Abs(options.SourcePath)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	if moduleRoot := findParentGoMod(filepath.Dir(absSource)); moduleRoot != "" {
		manifest.ModuleSHA256, err = fileSHA256(filepath.Join(moduleRoot, "go.mod"))
		if err != nil {
			return fmt.Errorf("hash go.mod: %w", err)
		}
		if sumHash, err := fileSHA256(filepath.Join(moduleRoot, "go.sum")); err == nil {
			manifest.ModuleSumSHA256 = sumHash
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("hash go.sum: %w", err)
		}
	}

	base := strings.TrimSuffix(filepath.Base(options.SourcePath), ".fw")
	for _, target := range options.Targets {
		name := base + "-" + target.GOOS + "-" + target.GOARCH
		if target.GOOS == "windows" {
			name += ".exe"
		}
		binaryPath := filepath.Join(stageDir, name)
		build := exec.Command(goBinary, "build", "-trimpath", "-buildvcs=false", "-mod=readonly",
			"-ldflags=-buildid=", "-o", binaryPath, filepath.Join(tmpGoDir, "main.go"))
		build.Dir = tmpGoDir
		build.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOFLAGS=",
			"CGO_ENABLED=0", "GOEXPERIMENT=", "GOOS="+target.GOOS, "GOARCH="+target.GOARCH,
			"GOAMD64=v1", "GOARM=7")
		if output, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s/%s: %w\n%s", target.GOOS, target.GOARCH, err, strings.TrimSpace(string(output)))
		}
		info, err := os.Stat(binaryPath)
		if err != nil {
			return fmt.Errorf("inspect package binary: %w", err)
		}
		hash, err := fileSHA256(binaryPath)
		if err != nil {
			return fmt.Errorf("hash package binary: %w", err)
		}
		manifest.Artifacts = append(manifest.Artifacts, packageArtifact{
			File: name, GOOS: target.GOOS, GOARCH: target.GOARCH,
			SHA256: hash, Size: info.Size(), CGOEnabled: false, Static: target.GOOS == "linux",
		})
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode package manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(stageDir, "manifest.json"), manifestData, 0644); err != nil {
		return fmt.Errorf("write package manifest: %w", err)
	}
	if err := os.Mkdir(outputDir, 0755); err != nil {
		return fmt.Errorf("reserve package output directory: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			if err := os.RemoveAll(outputDir); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove incomplete package output: %w", err))
			}
		}
	}()
	for _, artifact := range manifest.Artifacts {
		if err := os.Rename(filepath.Join(stageDir, artifact.File), filepath.Join(outputDir, artifact.File)); err != nil {
			return fmt.Errorf("publish package binary: %w", err)
		}
	}
	if err := os.Rename(filepath.Join(stageDir, "manifest.json"), filepath.Join(outputDir, "manifest.json")); err != nil {
		return fmt.Errorf("publish package manifest: %w", err)
	}
	complete = true
	return nil
}

func bytesSHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
