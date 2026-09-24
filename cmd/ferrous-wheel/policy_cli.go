package main

import (
	"errors"
	"fmt"
	gobuild "go/build"
	"io"
	"os"
	"runtime"
	"strings"

	"m31labs.dev/ferrous-wheel/policy"
)

type loadedPolicy struct {
	config policy.Config
	sha256 string
}

func loadCLIPolicy(path string) (*loadedPolicy, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy %s: %w", path, err)
	}
	config, err := policy.ParseConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse policy %s: %w", path, err)
	}
	return &loadedPolicy{config: config, sha256: bytesSHA256(data)}, nil
}

type policyDeniedError struct {
	diagnostics []policy.Diagnostic
}

func (e *policyDeniedError) Error() string {
	lines := make([]string, len(e.diagnostics))
	for i, diagnostic := range e.diagnostics {
		lines[i] = diagnostic.Error()
	}
	return strings.Join(lines, "\n")
}

func printCommandError(w io.Writer, err error) {
	var denied *policyDeniedError
	if errors.As(err, &denied) {
		fmt.Fprintln(w, denied)
		return
	}
	fmt.Fprintf(w, "error: %v\n", err)
}

func (p *loadedPolicy) checkTarget(goos string) error {
	if p != nil && p.config.FileRoots != nil && goos != runtime.GOOS {
		return fmt.Errorf("policy: cross-target file roots are unsupported (build host %s, target %s)", runtime.GOOS, goos)
	}
	return nil
}

func (p *loadedPolicy) checkSource(label string, source []byte) error {
	if p == nil {
		return nil
	}
	diagnostics, err := policy.Check(source, label, p.config)
	if err != nil {
		return fmt.Errorf("check policy for %s: %w", label, err)
	}
	if len(diagnostics) > 0 {
		return &policyDeniedError{diagnostics: diagnostics}
	}
	return nil
}

func (p *loadedPolicy) checkPackage(pkg *fwSourcePackage) error {
	if p == nil {
		return nil
	}
	var all []policy.Diagnostic
	for _, file := range pkg.files {
		diagnostics, err := policy.Check(file.source, file.relative, p.config)
		if err != nil {
			return fmt.Errorf("check policy for %s: %w", file.path, err)
		}
		all = append(all, diagnostics...)
	}
	if len(all) > 0 {
		return &policyDeniedError{diagnostics: all}
	}
	return nil
}

// checkInput reads every reachable .fw file before command setup. Staging
// checks the same source again and uses those checked bytes for transpilation.
func (p *loadedPolicy) checkInput(path string, target gobuild.Context) error {
	if p == nil {
		return nil
	}
	if err := p.checkTarget(target.GOOS); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect source: %w", err)
	}
	if info.IsDir() {
		pkg, err := discoverFWPackage(path, target)
		if err != nil {
			return err
		}
		return p.checkPackage(pkg)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	return p.checkSource(path, source)
}
