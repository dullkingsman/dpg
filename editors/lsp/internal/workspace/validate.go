package workspace

import (
	"encoding/json"
	"os/exec"
	"strings"
)

type validateResult struct {
	Cluster  string          `json:"cluster"`
	Database string          `json:"database"`
	Objects  int             `json:"objects"`
	Errors   []diagnosticRaw `json:"errors"`
	Warnings []diagnosticRaw `json:"warnings"`
}

type diagnosticRaw struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Col     int    `json:"col,omitempty"`
}

// runValidate shells out to `dpg validate --format json <tmpPath>` and returns
// diagnostics remapped back to originalPath.
func runValidate(root, tmpPath, originalPath string) []Diagnostic {
	// Pass tmpPath as a positional argument so dpg validates only that file,
	// without needing a full project on disk. The JSON output will reference
	// tmpPath in the `file` field; fileMatches remaps it to originalPath.
	cmd := exec.Command("dpg", "validate", "--format", "json", tmpPath)
	if root != "" {
		cmd.Dir = root
	}

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// dpg validate exits non-zero when there are errors; stdout has JSON
			out = append(out, exitErr.Stderr...)
		} else {
			return nil
		}
	}

	var results []validateResult
	// dpg may emit multiple JSON objects (one per database)
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for decoder.More() {
		var r validateResult
		if err := decoder.Decode(&r); err != nil {
			break
		}
		results = append(results, r)
	}

	var diags []Diagnostic
	for _, r := range results {
		for _, e := range r.Errors {
			if !fileMatches(e.File, originalPath, tmpPath) {
				continue
			}
			diags = append(diags, Diagnostic{
				Rule:    e.Rule,
				Message: e.Message,
				File:    originalPath,
				Line:    e.Line,
				Col:     e.Col,
				IsError: true,
			})
		}
		for _, w := range r.Warnings {
			if !fileMatches(w.File, originalPath, tmpPath) {
				continue
			}
			diags = append(diags, Diagnostic{
				Rule:    w.Rule,
				Message: w.Message,
				File:    originalPath,
				Line:    w.Line,
				Col:     w.Col,
				IsError: false,
			})
		}
	}
	return diags
}

func fileMatches(diagFile, original, tmp string) bool {
	if diagFile == "" {
		return true
	}
	return diagFile == original || diagFile == tmp
}
