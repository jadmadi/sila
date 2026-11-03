package loaders

import (
	"encoding/json"
	"io"
	"net/http"
)

// NewHookHTTPHandler creates an HTTP handler for POST /hook and POST /api/hook.
// Clamps payloads at 16 KiB and spools events non-blockingly (<5ms).
func NewHookHTTPHandler(spooler *Spooler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error": "method not allowed; use POST"}`, http.StatusMethodNotAllowed)
			return
		}

		// Read up to MaxHookPayloadBytes + 1 to detect oversized payloads
		lr := io.LimitReader(r.Body, int64(MaxHookPayloadBytes)+1)
		data, err := io.ReadAll(lr)
		if err != nil {
			http.Error(w, `{"error": "failed to read request body"}`, http.StatusInternalServerError)
			return
		}

		if len(data) > MaxHookPayloadBytes {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error": "payload exceeds 16 KiB limit"}`))
			return
		}

		env, err := ParseHookEnvelope(data)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		if _, err := spooler.Spool(env); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "accepted",
			"event":      env.Event,
			"agent":      env.Agent,
			"session_id": env.SessionID,
			"timestamp":  env.Timestamp,
		})
	}
}
