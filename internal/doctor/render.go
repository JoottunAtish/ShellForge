package doctor

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// jsonReport is the --json document. ok is carried separately so that a
// caller scripting against it does not have to reimplement the
// warn-does-not-fail rule.
type jsonReport struct {
	OK       bool         `json:"ok"`
	Platform string       `json:"platform"`
	Version  string       `json:"version"`
	Results  []jsonResult `json:"results"`
	Fixed    []string     `json:"fixed,omitempty"`
	Refused  []string     `json:"refused,omitempty"`
}

type jsonResult struct {
	ID          string `json:"id"`
	Status      Level  `json:"status"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation"`
	DocAnchor   string `json:"doc_anchor"`
	Fixable     bool   `json:"fixable"`
}

// RenderTable writes the human-readable report. Colour follows
// ux.ColorEnabled(w).
//
// Layout is fixed-width padding computed from the data, not text/tabwriter:
// the status marker (ok, warn, fail) is padded to six columns, then the
// probe id padded to the widest id in this report plus two, then the
// detail. Markers are words rather than glyphs, so a Windows console with
// no VT support still renders them correctly. Non-ok rows are followed by
// an indented "Fix:" line and an indented "More detail:" line built from
// ux.DocURL; an ok row gets neither.
func RenderTable(w io.Writer, r Report) error {
	color := ux.ColorEnabled(w)

	widestID := 0
	for _, res := range r.Results {
		if len(res.ID) > widestID {
			widestID = len(res.ID)
		}
	}
	idWidth := widestID + 2

	if _, err := fmt.Fprintf(w, "shellforge doctor: %s, %s\n\n", r.Platform, nonEmpty(r.Version, "dev")); err != nil {
		return err
	}

	var okCount, warnCount, failCount int
	for _, res := range r.Results {
		if _, err := fmt.Fprintf(w, "%s %-*s %s\n", padMarker(res.Status, color), idWidth, res.ID, res.Detail); err != nil {
			return err
		}
		if res.Status != OK {
			if _, err := fmt.Fprintf(w, "    Fix: %s\n", res.Remediation); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "    More detail: %s\n", ux.DocURL(res.DocAnchor)); err != nil {
				return err
			}
		}
		switch res.Status {
		case OK:
			okCount++
		case Warn:
			warnCount++
		case Fail:
			failCount++
		}
	}

	_, err := fmt.Fprintf(w, "\n%d ok, %d warn, %d fail\n", okCount, warnCount, failCount)
	return err
}

// padMarker renders a Level as its lowercase word, left-padded to six
// columns, wrapped in colour when color is true. Padding happens before
// colouring so the escape codes never affect the visible column width.
func padMarker(l Level, color bool) string {
	word := fmt.Sprintf("%-6s", l.String())
	if !color {
		return word
	}
	code := "1;32" // ok: green
	switch l {
	case Warn:
		code = "1;33"
	case Fail:
		code = "1;31"
	}
	return "\x1b[" + code + "m" + word + "\x1b[0m"
}

// RenderJSON writes the machine-readable report, indented two spaces, with
// a trailing newline.
func RenderJSON(w io.Writer, r Report) error {
	return renderJSON(w, r, nil, nil)
}

// RenderJSONWithFixOutcome is RenderJSON plus the fixed and refused lists
// --fix produced, so the whole run stays one parseable JSON document.
//
// The original plan described this seam as an unexported renderJSON call
// made directly by "the CLI"; that call cannot cross the package boundary
// from cmd/shellforge, which is a different package, so this thin exported
// wrapper is the fix. See the deviations note in the implementation report.
func RenderJSONWithFixOutcome(w io.Writer, r Report, fixed, refused []string) error {
	return renderJSON(w, r, fixed, refused)
}

// renderJSON is the seam both exported JSON renderers share.
func renderJSON(w io.Writer, r Report, fixed, refused []string) error {
	results := make([]jsonResult, 0, len(r.Results))
	for _, res := range r.Results {
		results = append(results, jsonResult{
			ID:          res.ID,
			Status:      res.Status,
			Detail:      res.Detail,
			Remediation: res.Remediation,
			DocAnchor:   res.DocAnchor,
			Fixable:     res.Fixable,
		})
	}

	doc := jsonReport{
		OK:       !r.Failed(),
		Platform: r.Platform,
		Version:  r.Version,
		Results:  results,
		Fixed:    fixed,
		Refused:  refused,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
