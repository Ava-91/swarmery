package api

// connectors (MCP servers): a read + add/remove surface over Claude Code's own
// MCP configuration, read through internal/mcpcfg (which shells to `claude mcp
// …` with an injectable, test-isolated runner). GET is the unfenced read feed;
// POST/DELETE mutate and carry the same requireLocalOrigin D4 hardening as every
// other write endpoint. All argument passing is argv-slice through mcpcfg —
// user input never reaches a shell.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/atretyak1985/swarmery/tools/swarmery/internal/mcpcfg"
)

// connReader is attached once at startup (cmdServe). nil (mock/serve-less
// builds) makes every connectors endpoint answer 503.
var connReader *mcpcfg.Reader

// AttachConnectorReader wires the MCP-config reader; tests attach a reader built
// on a fake runner.
func AttachConnectorReader(r *mcpcfg.Reader) { connReader = r }

// connectorsCmdTimeout bounds each `claude mcp …` invocation so a hung CLI
// cannot wedge a request.
const connectorsCmdTimeout = 15 * time.Second

type connectorDTO struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Scope     string `json:"scope"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
	Source    string `json:"source"`
}

type connectorsResponse struct {
	Connectors []connectorDTO `json:"connectors"`
}

type addConnectorRequest struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	URL       string   `json:"url"`
	Scope     string   `json:"scope"`
}

func toConnectorDTOs(servers []mcpcfg.Server) []connectorDTO {
	out := make([]connectorDTO, 0, len(servers))
	for _, s := range servers {
		out = append(out, connectorDTO{
			Name:      s.Name,
			Transport: string(s.Transport),
			Scope:     string(s.Scope),
			Status:    string(s.Status),
			Detail:    s.Detail,
			Source:    s.Source,
		})
	}
	return out
}

// listConnectors is the shared read used by GET and by the refreshed response
// returned from a successful mutation.
func (h *Handler) writeConnectorList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), connectorsCmdTimeout)
	defer cancel()
	servers, err := connReader.List(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, connectorsResponse{Connectors: toConnectorDTOs(servers)}, nil)
}

// connectors handles GET /api/connectors.
func (h *Handler) connectors(w http.ResponseWriter, r *http.Request) {
	if connReader == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "connector reader not attached"})
		return
	}
	h.writeConnectorList(w, r)
}

// addConnector handles POST /api/connectors.
func (h *Handler) addConnector(w http.ResponseWriter, r *http.Request) {
	if connReader == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "connector reader not attached"})
		return
	}
	var req addConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeClientErr(w, http.StatusBadRequest, "malformed request body")
		return
	}
	spec := mcpcfg.AddSpec{
		Name:      req.Name,
		Transport: mcpcfg.Transport(req.Transport),
		Command:   req.Command,
		Args:      req.Args,
		URL:       req.URL,
		Scope:     mcpcfg.Scope(req.Scope),
	}
	ctx, cancel := context.WithTimeout(r.Context(), connectorsCmdTimeout)
	defer cancel()
	if err := connReader.Add(ctx, spec); err != nil {
		// Validation failures are the caller's fault → 400; anything else
		// (CLI exec failure) is a 500.
		if errors.Is(err, mcpcfg.ErrInvalidInput) {
			writeClientErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErr(w, err)
		return
	}
	h.writeConnectorList(w, r)
}

// removeConnector handles DELETE /api/connectors/{name}?scope=…
func (h *Handler) removeConnector(w http.ResponseWriter, r *http.Request) {
	if connReader == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": "connector reader not attached"})
		return
	}
	name := r.PathValue("name")
	scope := mcpcfg.Scope(r.URL.Query().Get("scope"))
	ctx, cancel := context.WithTimeout(r.Context(), connectorsCmdTimeout)
	defer cancel()
	if err := connReader.Remove(ctx, name, scope); err != nil {
		if errors.Is(err, mcpcfg.ErrInvalidInput) {
			writeClientErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeErr(w, err)
		return
	}
	h.writeConnectorList(w, r)
}
