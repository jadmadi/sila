package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/jadmadi/sila/internal/man"
)

const manResourceURI = "sila://man"

func (s *Server) handleResourcesList(req JSONRPCRequest) {
	type resourceEntry struct {
		URI      string `json:"uri"`
		Name     string `json:"name"`
		MimeType string `json:"mimeType"`
	}
	entries := []resourceEntry{{URI: manResourceURI, Name: "Sila manual index", MimeType: "text/markdown"}}
	for _, page := range man.List() {
		entries = append(entries, resourceEntry{
			URI:      manResourceURI + "/" + page.Topic,
			Name:     "sila man " + page.Topic,
			MimeType: "text/markdown",
		})
	}
	s.sendResult(req.ID, map[string]any{"resources": entries})
}

func (s *Server) handleResourcesRead(req JSONRPCRequest) {
	var params struct {
		URI string `json:"uri"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}
	uri := strings.TrimSpace(params.URI)
	if decoded, err := url.PathUnescape(uri); err == nil {
		uri = decoded
	}
	uri = strings.TrimSuffix(uri, "/")
	if uri == manResourceURI {
		var builder strings.Builder
		builder.WriteString("# Sila manual index\n\n")
		for _, page := range man.List() {
			builder.WriteString(fmt.Sprintf("- [%s](%s/%s) — %s\n", page.Topic, manResourceURI, page.Topic, page.Title))
		}
		s.sendResult(req.ID, resourceContents(uri, builder.String()))
		return
	}
	if !strings.HasPrefix(uri, manResourceURI+"/") {
		s.sendError(req.ID, -32602, "unknown man topic", uri)
		return
	}
	topic := strings.TrimPrefix(uri, manResourceURI+"/")
	page, ok := man.Lookup(topic)
	if !ok {
		s.sendError(req.ID, -32602, "unknown man topic "+topic, "")
		return
	}
	s.sendResult(req.ID, resourceContents(manResourceURI+"/"+page.Topic, page.Body))
}

func resourceContents(uri, text string) map[string]any {
	return map[string]any{
		"contents": []map[string]any{{
			"uri": uri, "mimeType": "text/markdown", "text": text,
		}},
	}
}
