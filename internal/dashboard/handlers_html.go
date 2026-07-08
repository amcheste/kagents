package dashboard

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	claudev1alpha1 "github.com/amcheste/kagents/api/v1alpha1"
)

// routeRoot dispatches "/" to the list view and 404s anything else that
// reaches the bare handler. We register `/` so net/http's mux uses this as
// the catch-all, then filter explicitly so unknown paths don't render the
// list view by accident.
func (s *Server) routeRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFoundHTML(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.htmlListView(w, r)
}

// htmlListView renders the full list page. The rendered tbody is also
// available as a fragment at /api/htmx/teams; the page's polling loop hits
// that fragment, so the full page renders only on initial load and full
// refresh.
func (s *Server) htmlListView(w http.ResponseWriter, r *http.Request) {
	teams, err := s.listTeamsForView(r)
	if err != nil {
		s.htmlError(w, http.StatusInternalServerError, "Listing teams failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := renderPage(w, "list", pageData{
		Title:    "Teams",
		Subtitle: "live operator dashboard",
		Data:     teams,
	}); err != nil {
		log.FromContext(r.Context()).Error(err, "render list view")
	}
}

// routeDetailHTML matches /teams/{ns}/{name} and renders the detail page.
// Any deeper or shorter path drops to 404.
func (s *Server) routeDetailHTML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/teams/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		s.notFoundHTML(w, r)
		return
	}
	team, err := s.getTeamForView(r, parts[0], parts[1])
	if err != nil {
		if errors.IsNotFound(err) {
			s.htmlError(w, http.StatusNotFound, "Team not found",
				fmt.Sprintf("AgentTeam %s/%s does not exist or has been deleted.", parts[0], parts[1]))
			return
		}
		s.htmlError(w, http.StatusInternalServerError, "Loading team failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := renderPage(w, "detail", pageData{
		Title:    team.Name,
		Subtitle: fmt.Sprintf("%s/%s", team.Namespace, team.Name),
		Data:     team,
	}); err != nil {
		log.FromContext(r.Context()).Error(err, "render detail view")
	}
}

// htmxListRows returns the <tbody> contents for the list page. Used by
// HTMX's `hx-trigger="every 5s"` polling on the list view — the response
// is HTML, not JSON, so the swap is a one-step `innerHTML` replacement.
func (s *Server) htmxListRows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	teams, err := s.listTeamsForView(r)
	if err != nil {
		// HTMX swaps don't surface response bodies on errors by default;
		// returning a small error row at least makes the failure visible.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		fmt.Fprintf(w, `<tr><td colspan="7" class="px-4 py-2 text-red-600 text-xs">listing failed: %s</td></tr>`, html.EscapeString(err.Error()))

		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := renderFragment(w, "list_rows", teams); err != nil {
		log.FromContext(r.Context()).Error(err, "render list_rows fragment")
	}
}

// htmxDetailBody returns the inner detail body for the detail page's
// polling swap. The detail.html page wraps a single hx-get'd <div> around
// this fragment so the entire body refreshes in one swap.
func (s *Server) htmxDetailBody(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/htmx/teams/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	team, err := s.getTeamForView(r, parts[0], parts[1])
	if err != nil {
		if errors.IsNotFound(err) {
			http.Error(w, "Team not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := renderFragment(w, "detail_body", team); err != nil {
		log.FromContext(r.Context()).Error(err, "render detail_body fragment")
	}
}

// notFoundHTML renders the 404 page. Used by the catch-all routes that
// can't satisfy a more specific match.
func (s *Server) notFoundHTML(w http.ResponseWriter, r *http.Request) {
	s.htmlError(w, http.StatusNotFound, "Page not found",
		fmt.Sprintf("No route matches %s.", r.URL.Path))
}

// htmlError renders the shared error page with the provided status, title,
// and message. Distinct from writeError (which returns JSON) — this one
// keeps the chrome consistent for users who hit a bad route.
func (s *Server) htmlError(w http.ResponseWriter, code int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = renderPage(w, "error", pageData{
		Title:    title,
		Subtitle: fmt.Sprintf("error %d", code),
		Data:     errorData{Code: code, Title: title, Message: message},
	})
}

// listTeamsForView centralizes the namespace-scoping logic shared between
// the JSON and HTML list endpoints, so they can never disagree on which
// teams a given request sees.
func (s *Server) listTeamsForView(r *http.Request) ([]claudev1alpha1.AgentTeam, error) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = s.Namespace
	}
	var teams claudev1alpha1.AgentTeamList
	opts := []client.ListOption{}
	if ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := s.CRClient.List(r.Context(), &teams, opts...); err != nil {
		return nil, err
	}
	return teams.Items, nil
}

// getTeamForView fetches a single AgentTeam for HTML rendering. Splits
// the NotFound-vs-other-error decision out of the route handler so the
// HTML and HTMX-fragment routes share semantics.
func (s *Server) getTeamForView(r *http.Request, ns, name string) (*claudev1alpha1.AgentTeam, error) {
	var team claudev1alpha1.AgentTeam
	if err := s.CRClient.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &team); err != nil {
		return nil, err
	}
	return &team, nil
}
