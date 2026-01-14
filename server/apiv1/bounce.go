package apiv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"time"

	"github.com/axllent/mailpit/config"
	"github.com/axllent/mailpit/internal/logger"
	"github.com/axllent/mailpit/internal/smtpd"
	"github.com/axllent/mailpit/internal/storage"
	"github.com/gorilla/mux"
	"github.com/lithammer/shortuuid/v4"
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

	msg, err := storage.GetMessage(id)
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

	// Determine bounce address: ReturnPath -> ReplyTo -> From
	var bounceAddress string
	var bounceAddressName string

	if msg.ReturnPath != "" {
		bounceAddress = msg.ReturnPath
		bounceAddressName = "Return-Path"
	} else if len(msg.ReplyTo) > 0 && msg.ReplyTo[0] != nil {
		bounceAddress = msg.ReplyTo[0].Address
		bounceAddressName = "Reply-To"
	} else if msg.From != nil {
		bounceAddress = msg.From.Address
		bounceAddressName = "From"
	} else {
		httpError(w, "No Return-Path, Reply-To, or From address found in message")
		return
	}

	logger.Log().Debugf("[bounce] bounce address: %s", bounceAddress)
	logger.Log().Debugf("[bounce] bounce address name: %s", bounceAddressName)

	// Parse bounce address
	bounceAddr, err := mail.ParseAddress(bounceAddress)
	if err != nil {
		httpError(w, fmt.Sprintf("Invalid %s address: %s", bounceAddressName, err.Error()))
		return
	}

	logger.Log().Debugf("[bounce] parsed bounce address: %s", bounceAddr.Address)

	// Get raw message for DSN construction
	rawMsg, err := storage.GetMessageRaw(id)
	if err != nil {
		httpError(w, "Failed to get raw message: "+err.Error())
		return
	}

	// Construct DSN message
	dsnMsg, err := buildDSNMessage(msg, rawMsg, bounceAddr.Address, data.Reason, data.Code)
	if err != nil {
		httpError(w, "Failed to construct DSN message: "+err.Error())
		return
	}

	// Send DSN message via bounce relay
	if err := smtpd.BounceRelay(bounceAddr.Address, dsnMsg); err != nil {
		logger.Log().Errorf("[bounce] error sending DSN: %s", err.Error())
		httpError(w, "Failed to send bounce message: "+err.Error())
		return
	}

	logger.Log().Debugf("[bounce] sent bounce for message %s to %s (%s, reason: %s, code: %s) via %s:%d", id, bounceAddr.Address, bounceAddressName, data.Reason, data.Code, config.SMTPBounceConfig.Host, config.SMTPBounceConfig.Port)

	w.Header().Add("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

// buildDSNMessage constructs a Delivery Status Notification (DSN) message according to RFC 3464
func buildDSNMessage(originalMsg *storage.Message, originalRaw []byte, returnPath string, reason string, code string) ([]byte, error) {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "mailpit"
	}

	// Determine From address for bounce message
	bounceFrom := config.SMTPBounceConfig.From
	if bounceFrom == "" {
		// Default to standard MAILER-DAEMON format
		bounceFrom = fmt.Sprintf("Mail Delivery Subsystem <MAILER-DAEMON@%s>", hostname)
	}

	// Parse original message to get headers
	reader := bytes.NewReader(originalRaw)
	originalParsed, err := mail.ReadMessage(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse original message: %w", err)
	}

	messageID := originalParsed.Header.Get("Message-ID")
	if messageID == "" {
		messageID = "<" + shortuuid.New() + "@mailpit>"
	}

	// Extract original recipient addresses
	var recipients []string
	for _, addr := range originalMsg.To {
		recipients = append(recipients, addr.Address)
	}
	for _, addr := range originalMsg.Cc {
		recipients = append(recipients, addr.Address)
	}
	for _, addr := range originalMsg.Bcc {
		recipients = append(recipients, addr.Address)
	}

	// Build DSN message
	var dsn bytes.Buffer

	// Headers
	dsn.WriteString("Return-Path: <>\r\n")
	dsn.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	dsn.WriteString(fmt.Sprintf("From: %s\r\n", bounceFrom))
	dsn.WriteString(fmt.Sprintf("To: %s\r\n", returnPath))
	dsn.WriteString("Subject: Mail delivery failed: returning message to sender\r\n")
	dsn.WriteString(fmt.Sprintf("Message-ID: <%s@%s>\r\n", shortuuid.New(), hostname))
	dsn.WriteString("MIME-Version: 1.0\r\n")
	dsn.WriteString("Content-Type: multipart/report; report-type=delivery-status; boundary=\"BOUNDARY\"\r\n")
	dsn.WriteString("\r\n")

	// Human-readable part
	dsn.WriteString("--BOUNDARY\r\n")
	dsn.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	dsn.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	dsn.WriteString("\r\n")
	dsn.WriteString("This is the mail system at host " + hostname + ".\r\n\r\n")
	dsn.WriteString("I'm sorry to have to inform you that your message could not be delivered to one or more recipients.\r\n\r\n")
	dsn.WriteString(fmt.Sprintf("Reason: %s\r\n", reason))
	dsn.WriteString(fmt.Sprintf("SMTP Status Code: %s\r\n", code))
	dsn.WriteString("\r\n")
	dsn.WriteString("For further assistance, please send mail to postmaster.\r\n\r\n")
	dsn.WriteString("The mail system\r\n")
	dsn.WriteString("\r\n")

	// Machine-readable DSN part
	dsn.WriteString("--BOUNDARY\r\n")
	dsn.WriteString("Content-Type: message/delivery-status\r\n")
	dsn.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	dsn.WriteString("\r\n")
	dsn.WriteString(fmt.Sprintf("Reporting-MTA: dns; %s\r\n", hostname))
	dsn.WriteString(fmt.Sprintf("Arrival-Date: %s\r\n", time.Now().Format(time.RFC3339)))
	dsn.WriteString("\r\n")
	for _, recipient := range recipients {
		dsn.WriteString(fmt.Sprintf("Final-Recipient: rfc822; %s\r\n", recipient))
		dsn.WriteString("Action: failed\r\n")
		dsn.WriteString(fmt.Sprintf("Status: %s\r\n", code))
		dsn.WriteString(fmt.Sprintf("Diagnostic-Code: smtp; %s\r\n", reason))
		dsn.WriteString("\r\n")
	}

	// Original message part
	dsn.WriteString("--BOUNDARY\r\n")
	dsn.WriteString("Content-Type: message/rfc822\r\n")
	dsn.WriteString("Content-Transfer-Encoding: 7bit\r\n")
	dsn.WriteString("\r\n")
	dsn.Write(originalRaw)
	dsn.WriteString("\r\n")

	// End boundary
	dsn.WriteString("--BOUNDARY--\r\n")

	return dsn.Bytes(), nil
}
