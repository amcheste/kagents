package dashboard

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"strconv"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

//go:embed templates/*.html
var templatesFS embed.FS

// templateFuncs are exposed to all templates. Small bits of computed display
// logic that html/template's built-ins can't express (pointer deref,
// percentage math, lookup-in-slice).
var templateFuncs = template.FuncMap{
	// deref unwraps a *string to its value, returning "" for nil. Used for
	// Lifecycle.BudgetLimit — the CRD distinguishes "unset" from "" via a
	// pointer.
	"deref": func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	},

	// budgetPercent computes a 0..100 progress value for the team's budget
	// bar. Returns 0 when the team has no budget configured. Caps at 100
	// for over-budget UI state.
	"budgetPercent": func(team claudev1alpha1.AgentTeam) int {
		if team.Spec.Lifecycle == nil || team.Spec.Lifecycle.BudgetLimit == nil {
			return 0
		}
		limit, err := strconv.ParseFloat(*team.Spec.Lifecycle.BudgetLimit, 64)
		if err != nil || limit <= 0 {
			return 0
		}
		current, _ := strconv.ParseFloat(team.Status.EstimatedCost, 64)
		pct := int((current / limit) * 100)
		if pct > 100 {
			return 100
		}
		if pct < 0 {
			return 0
		}
		return pct
	},

	// teammateStatus pulls the matching TeammateStatus by name, returning
	// a zero-value (Phase="") entry when the teammate hasn't reported yet.
	"teammateStatus": func(statuses []claudev1alpha1.TeammateStatus, name string) claudev1alpha1.TeammateStatus {
		for _, s := range statuses {
			if s.Name == name {
				return s
			}
		}
		return claudev1alpha1.TeammateStatus{Name: name}
	},

	// pipelinePercent computes a 0..100 progress value for the pipeline
	// progress bar. Returns 0 when the pipeline reports no stages —
	// callers that gate the section on .Status.Pipeline being non-nil
	// won't reach this in that case, but the guard is cheap.
	"pipelinePercent": func(p *claudev1alpha1.PipelineStatus) int {
		if p == nil || p.StagesTotal == 0 {
			return 0
		}
		pct := (p.StagesCompleted * 100) / p.StagesTotal
		if pct > 100 {
			return 100
		}
		return pct
	},
}

// pageData wraps the typed payload in a render-time envelope so the layout
// can reach common fields (page title, subtitle) without each content block
// re-declaring them.
type pageData struct {
	Title    string
	Subtitle string
	Data     interface{}
}

// errorData is the payload shape for templates/error.html.
type errorData struct {
	Code    int
	Title   string
	Message string
}

// renderPage parses layout + the named content template (and any matching
// partial fragments) per request. We parse fresh rather than caching a
// merged template set because every content view defines its own block
// named "content" — html/template would silently overwrite all but the
// last if they were parsed into the same set.
//
// The cost is one ParseFS call per request; for a low-traffic dashboard
// this is well below the noise floor of the K8s API round-trips that
// dominate request time.
func renderPage(w io.Writer, contentName string, data pageData) error {
	files := []string{"templates/layout.html", "templates/" + contentName + ".html"}
	// Auto-include the partial that the page template embeds via
	// {{template "<name>"}}. Keeps the file list correct without each
	// caller having to know which partials a page uses.
	switch contentName {
	case "list":
		files = append(files, "templates/_list_rows.html")
	case "detail":
		files = append(files, "templates/_detail_body.html")
	}

	tpl, err := template.New("page").Funcs(templateFuncs).ParseFS(templatesFS, files...)
	if err != nil {
		return fmt.Errorf("parse templates for %q: %w", contentName, err)
	}
	return tpl.ExecuteTemplate(w, "layout", data)
}

// renderFragment renders just a named partial — used for HTMX swaps where
// the response is an HTML fragment, not a full page. fragmentName is the
// template name as defined inside the partial file (e.g. "list_rows" for
// templates/_list_rows.html which `{{define "list_rows"}}`s its content).
func renderFragment(w io.Writer, fragmentName string, data interface{}) error {
	file := "templates/_" + fragmentName + ".html"
	tpl, err := template.New("fragment").Funcs(templateFuncs).ParseFS(templatesFS, file)
	if err != nil {
		return fmt.Errorf("parse fragment %q: %w", fragmentName, err)
	}
	return tpl.ExecuteTemplate(w, fragmentName, data)
}
