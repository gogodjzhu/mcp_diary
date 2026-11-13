package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
)

// decodeJSON reads and decodes a request body, capped at 1 MiB.
func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
