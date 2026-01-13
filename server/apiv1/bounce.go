package apiv1

import (
	"encoding/json"
	"net/http"

	"github.com/axllent/mailpit/config"
	"github.com/axllent/mailpit/internal/storage"
	"github.com/gorilla/mux"
)

// BounceMessage (method: POST) will bounce a message with the specified reason and code.
func BounceMessage(w http.ResponseWriter, r *http.Request) {
	// swagger:route POST /api/v1/message/{ID}/bounce message BounceMessageParams
	//
	// # Bounce message
	//
	// Bounce a message with the specified reason and SMTP status code.
	//
	// The ID can be set to `latest` to reference the latest message.
	//
	//	Consumes:
	//	  - application/json
	//
	//	Produces:
	//	  - text/plain
	//
	//	Schemes: http, https
	//
	//	Responses:
	//	  200: OKResponse
	//    400: ErrorResponse
	//    404: NotFoundResponse

	if config.DemoMode {
		httpError(w, "this functionality has been disabled for demonstration purposes")
		return
	}

	vars := mux.Vars(r)

	id := vars["id"]

	_, err := storage.GetMessageRaw(id)
	if err != nil {
		fourOFour(w)
		return
	}

	decoder := json.NewDecoder(r.Body)

	var data struct {
		Reason string
		Code   string
	}

	if err := decoder.Decode(&data); err != nil {
		httpError(w, err.Error())
		return
	}

	if data.Reason == "" {
		httpError(w, "Reason is required")
		return
	}

	if data.Code == "" {
		httpError(w, "Code is required")
		return
	}

	// TODO: Implement bounce functionality
	// This is a placeholder for the actual bounce implementation

	w.Header().Add("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}
